package logger

import (
	"context"
	"log/slog"
	"os"

	"github.com/google/uuid"
)

const TRACE_ID_KEY = "traceId"

func Setup(logLevel slog.Leveler) {
	l := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(l)
}

func Get(ctx context.Context) *slog.Logger {
	id := ctx.Value(TRACE_ID_KEY)
	if id == nil {
		id = uuid.NewString()
	}
	return slog.With(TRACE_ID_KEY, id)
}

func GetWithValue(ctx context.Context) (context.Context, *slog.Logger) {
	id := ctx.Value(TRACE_ID_KEY)
	if id == nil {
		id = uuid.NewString()

		return context.WithValue(ctx, TRACE_ID_KEY, id), slog.With(TRACE_ID_KEY, id)
	}
	return ctx, slog.With(TRACE_ID_KEY, id)
}
