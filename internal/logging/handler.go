// Package logging provides trace correlation and sanitized structured logs.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"unicode"

	"go.opentelemetry.io/otel/trace"
)

const redacted = "[REDACTED]"

// Handler sanitizes records before serialization and injects trusted trace IDs.
// Bound attributes and groups are kept immutable, so derived loggers can be
// shared between goroutines. Trace IDs stay at the root, even with WithGroup.
type Handler struct {
	next    slog.Handler
	bound   []boundAttrs
	groups  []string
	secrets []string
}

type boundAttrs struct {
	groups []string
	attrs  []slog.Attr
}

func NewHandler(next slog.Handler) *Handler {
	var secrets []string
	for _, name := range []string{"ACCESS_TOKEN_SECRET", "REFRESH_TOKEN_SECRET", "DB_PASS", "REDIS_PASSWORD", "ANALYTICS_IP_SALT"} {
		if value := os.Getenv(name); value != "" {
			secrets = append(secrets, value)
		}
	}
	return &Handler{next: next, secrets: secrets}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	root := &attrNode{}
	for _, bound := range h.bound {
		root.addScoped(bound.groups, bound.attrs)
	}
	var attrs []slog.Attr
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, h.sanitizeAttr(attr))
		return true
	})
	root.addScoped(h.groups, attrs)
	clean := slog.NewRecord(record.Time.UTC(), record.Level, h.sanitizeString(record.Message), record.PC)
	for _, attr := range root.render() {
		// Sanitize group names created through WithGroup as well as attributes.
		clean.AddAttrs(h.sanitizeAttr(attr))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		clean.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.next.Handle(ctx, clean)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		clean = append(clean, h.sanitizeAttr(attr))
	}
	clone.bound = append(append([]boundAttrs(nil), h.bound...), boundAttrs{groups: append([]string(nil), h.groups...), attrs: clean})
	return &clone
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

// attrNode merges attributes belonging to the same group, avoiding duplicate
// group objects when WithAttrs and per-record attributes share a scope.
type attrNode struct {
	attrs    []slog.Attr
	children map[string]*attrNode
}

func (n *attrNode) group(name string) *attrNode {
	if n.children == nil {
		n.children = make(map[string]*attrNode)
	}
	if child := n.children[name]; child != nil {
		return child
	}
	child := &attrNode{}
	n.children[name] = child
	n.attrs = append(n.attrs, slog.Attr{Key: name, Value: slog.GroupValue()})
	return child
}

func (n *attrNode) addScoped(groups []string, attrs []slog.Attr) {
	for _, name := range groups {
		n = n.group(name)
	}
	for _, attr := range attrs {
		if attr.Equal(slog.Attr{}) {
			continue
		}
		if attr.Value.Kind() == slog.KindGroup {
			if attr.Key == "" {
				n.addScoped(nil, attr.Value.Group())
			} else {
				n.group(attr.Key).addScoped(nil, attr.Value.Group())
			}
		} else {
			n.attrs = append(n.attrs, attr)
		}
	}
}

func (n *attrNode) render() []slog.Attr {
	attrs := make([]slog.Attr, 0, len(n.attrs))
	for _, attr := range n.attrs {
		if attr.Value.Kind() == slog.KindGroup {
			child := n.children[attr.Key].render()
			if len(child) == 0 {
				continue
			}
			attr.Value = slog.GroupValue(child...)
		}
		attrs = append(attrs, attr)
	}
	return attrs
}

func normalized(key string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
}

func sensitiveKey(key string) bool {
	key = normalized(key)
	for _, part := range []string{"password", "passwd", "secret", "token", "credential", "authorization", "cookie", "apikey", "privatekey"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	switch key {
	case "ip", "clientip", "remoteip", "remoteaddr", "xforwardedfor", "xrealip", "username", "user", "userinfo", "pwd", "pass", "headers", "body", "request", "response", "query", "rawquery":
		return true
	}
	return false
}

func (h *Handler) sanitizeAttr(attr slog.Attr) slog.Attr {
	if attr.Equal(slog.Attr{}) {
		return attr
	}
	// Caller-supplied trace IDs cannot override the active span's identity.
	if key := normalized(attr.Key); key == "traceid" || key == "spanid" {
		return slog.Attr{}
	}
	attr.Key = h.sanitizeString(attr.Key)
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, redacted)
	}
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(h.sanitizeString(attr.Value.String()))
	case slog.KindGroup:
		children := make([]slog.Attr, 0, len(attr.Value.Group()))
		for _, child := range attr.Value.Group() {
			clean := h.sanitizeAttr(child)
			if !clean.Equal(slog.Attr{}) {
				children = append(children, clean)
			}
		}
		attr.Value = slog.GroupValue(children...)
	case slog.KindAny:
		// Driver errors and arbitrary structs/maps can embed command documents,
		// credentials or addresses. Keep error type; omit opaque object contents.
		if err, ok := attr.Value.Any().(error); ok {
			attr.Value = slog.StringValue(fmt.Sprintf("%T", err))
		} else {
			attr.Value = slog.StringValue(redacted)
		}
	}
	return attr
}

var (
	urlCredentials = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@]+@`)
	authValue = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9+/=._~-]+`)
	jwtValue = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\b`)
	secretAssignment = regexp.MustCompile(`(?i)\b(authorization|password|passwd|pwd|secret|access[_-]?token|refresh[_-]?token|api[_-]?key|token|client[_-]?secret|credentials)\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s&,;]+)`)
	ipv4Value = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ipv6Value = regexp.MustCompile(`(?i)[0-9a-f]*:[0-9a-f:.]+(?:%[a-z0-9_.-]+)?`)
)

func (h *Handler) sanitizeString(value string) string {
	for _, secret := range h.secrets {
		value = strings.ReplaceAll(value, secret, redacted)
	}
	value = urlCredentials.ReplaceAllString(value, "${1}"+redacted+"@")
	value = authValue.ReplaceAllString(value, "${1} "+redacted)
	value = jwtValue.ReplaceAllString(value, redacted)
	value = secretAssignment.ReplaceAllString(value, "${1}="+redacted)
	value = ipv6Value.ReplaceAllStringFunc(value, func(candidate string) string {
		if _, err := netip.ParseAddr(candidate); err == nil {
			return redacted
		}
		return candidate
	})
	return ipv4Value.ReplaceAllString(value, redacted)
}
