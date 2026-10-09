package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"go.mongodb.org/mongo-driver/mongo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// RecordError marks the active operation as failed and records its error event.
// Numeric MongoDB response codes are retained when the driver supplies them.
func RecordError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	code := "operation_failed"
	attrs := []attribute.KeyValue{attribute.String("error.type", fmt.Sprintf("%T", err))}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "cancelled"
	}
	var command mongo.CommandError
	var write mongo.WriteException
	var bulk mongo.BulkWriteException
	switch {
	case errors.As(err, &command):
		code = strconv.Itoa(int(command.Code))
		attrs = append(attrs, attribute.String("db.response.status_code", code))
	case errors.As(err, &write) && len(write.WriteErrors) > 0:
		code = strconv.Itoa(write.WriteErrors[0].Code)
		attrs = append(attrs, attribute.String("db.response.status_code", code))
	case errors.As(err, &write) && write.WriteConcernError != nil:
		code = strconv.Itoa(write.WriteConcernError.Code)
		attrs = append(attrs, attribute.String("db.response.status_code", code))
	case errors.As(err, &bulk) && len(bulk.WriteErrors) > 0:
		code = strconv.Itoa(bulk.WriteErrors[0].Code)
		attrs = append(attrs, attribute.String("db.response.status_code", code))
	case errors.As(err, &bulk) && bulk.WriteConcernError != nil:
		code = strconv.Itoa(bulk.WriteConcernError.Code)
		attrs = append(attrs, attribute.String("db.response.status_code", code))
	}
	attrs = append(attrs, attribute.String("error.code", code))
	if code != "operation_failed" {
		attrs = append(attrs, attribute.String("error.type", code))
	}
	span.SetAttributes(attrs...)
	span.RecordError(err)
	span.SetStatus(codes.Error, code)
}
