package telemetry

import (
	"context"
	"errors"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type mongoSpanCaptureKey struct{}
type mongoCommandKey struct {
	connection string
	request    int64
}

// MongoMonitor retains otelmongo's command spans and statement attributes while
// recording failed commands and write errors returned in successful replies.
func MongoMonitor() *event.CommandMonitor {
	base := otelmongo.NewMonitor(
		otelmongo.WithCommandAttributeDisabled(false),
		otelmongo.WithTracerProvider(mongoCaptureProvider{otel.GetTracerProvider()}),
	)
	var pending sync.Map
	finish := func(ctx context.Context, event *event.CommandFinishedEvent) context.Context {
		if span, ok := pending.LoadAndDelete(mongoCommandKey{event.ConnectionID, event.RequestID}); ok {
			return trace.ContextWithSpan(ctx, span.(trace.Span))
		}
		return trace.ContextWithSpan(ctx, trace.SpanFromContext(context.Background()))
	}
	return &event.CommandMonitor{
		Started: func(ctx context.Context, event *event.CommandStartedEvent) {
			var span trace.Span
			base.Started(context.WithValue(ctx, mongoSpanCaptureKey{}, &span), event)
			if span != nil {
				pending.Store(mongoCommandKey{event.ConnectionID, event.RequestID}, span)
			}
		},
		Succeeded: func(ctx context.Context, event *event.CommandSucceededEvent) {
			ctx = finish(ctx, &event.CommandFinishedEvent)
			if failure := mongoWriteFailure(event.Reply); failure != nil {
				RecordError(ctx, failure)
			}
			base.Succeeded(ctx, event)
		},
		Failed: func(ctx context.Context, event *event.CommandFailedEvent) {
			ctx = finish(ctx, &event.CommandFinishedEvent)
			RecordError(ctx, errors.New(event.Failure))
			code := "mongo_command_failed"
			// The v1 driver's failure event provides a message with a code name
			// such as (Unauthorized), rather than a numeric error code.
			if strings.HasPrefix(event.Failure, "(") {
				if end := strings.IndexByte(event.Failure, ')'); end > 1 {
					name := event.Failure[1:end]
					if len(name) <= 64 && !strings.ContainsAny(name, " \t\r\n") {
						code = name
					}
				}
			}
			trace.SpanFromContext(ctx).SetAttributes(attribute.String("error.type", "mongo_command_error"), attribute.String("error.code", code))
			base.Failed(ctx, event)
		},
	}
}

// A MongoDB write command can succeed at the protocol level while carrying a
// failed write or write concern. These failures must mark the command span.
func mongoWriteFailure(reply bson.Raw) error {
	var failure bson.Raw
	if array, ok := reply.Lookup("writeErrors").ArrayOK(); ok {
		if entries, err := array.Values(); err == nil && len(entries) > 0 {
			failure, _ = entries[0].DocumentOK()
		}
	}
	if len(failure) == 0 {
		failure, _ = reply.Lookup("writeConcernError").DocumentOK()
	}
	if len(failure) == 0 {
		return nil
	}
	code, hasCode := failure.Lookup("code").Int32OK()
	if large, ok := failure.Lookup("code").Int64OK(); ok {
		code = int32(large)
		hasCode = true
	}
	message, _ := failure.Lookup("errmsg").StringValueOK()
	name, _ := failure.Lookup("codeName").StringValueOK()
	if !hasCode {
		return errors.New(message)
	}
	return mongo.CommandError{Code: code, Message: message, Name: name}
}

type mongoCaptureProvider struct{ trace.TracerProvider }

func (p mongoCaptureProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return mongoCaptureTracer{p.TracerProvider.Tracer(name, options...)}
}

type mongoCaptureTracer struct{ trace.Tracer }

func (t mongoCaptureTracer) Start(ctx context.Context, name string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, span := t.Tracer.Start(ctx, name, options...)
	if target, ok := ctx.Value(mongoSpanCaptureKey{}).(*trace.Span); ok {
		*target = span
	}
	return ctx, span
}
