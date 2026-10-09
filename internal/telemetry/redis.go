package telemetry

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// RedisErrorHook runs inside redisotel's span and adds bounded error codes.
// Register it after InstrumentTracing. Redis cache misses remain successful.
type RedisErrorHook struct{}

func (RedisErrorHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := next(ctx, network, address)
		annotateRedisError(ctx, err)
		return conn, err
	}
}

func (RedisErrorHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		annotateRedisError(ctx, err)
		return err
	}
}

func (RedisErrorHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		annotateRedisError(ctx, err)
		return err
	}
}

func annotateRedisError(ctx context.Context, err error) {
	if err == nil || errors.Is(err, redis.Nil) {
		return
	}
	code := "redis_command_failed"
	switch {
	case errors.Is(err, context.Canceled):
		code = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	default:
		prefix, _, _ := strings.Cut(err.Error(), " ")
		switch prefix {
		case "ERR", "WRONGTYPE", "NOAUTH", "WRONGPASS", "NOPERM", "READONLY", "OOM", "BUSY", "NOSCRIPT", "NOGROUP", "LOADING", "MASTERDOWN", "CLUSTERDOWN", "CROSSSLOT", "MOVED", "ASK", "TRYAGAIN":
			code = prefix
		}
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("error.type", code), attribute.String("error.code", code))
}
