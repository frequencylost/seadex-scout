package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Handler tees every record at Info or above into a Store, then passes it on
// to the wrapped handler when that handler's own level lets it through. The
// dashboard therefore keeps working at log.level warn, which would otherwise
// drop the Info lines it reads.
type Handler struct {
	inner  slog.Handler
	store  *Store
	attrs  []slog.Attr
	prefix string
}

// NewHandler wraps inner so each record also reaches store.
func NewHandler(inner slog.Handler, store *Store) *Handler {
	return &Handler{inner: inner, store: store}
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo || h.inner.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelInfo {
		h.store.Observe(h.line(&r))
	}
	if h.inner.Enabled(ctx, r.Level) {
		return h.inner.Handle(ctx, r)
	}
	return nil
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.inner = h.inner.WithAttrs(attrs)
	next.attrs = make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	next.attrs = append(next.attrs, h.attrs...)
	for _, a := range attrs {
		next.attrs = append(next.attrs, slog.Attr{Key: h.prefix + a.Key, Value: a.Value})
	}
	return &next
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.inner = h.inner.WithGroup(name)
	next.prefix = h.prefix + name + "."
	return &next
}

func (h *Handler) line(r *slog.Record) Line {
	attrs := make(map[string]any, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		putAttr(attrs, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		putAttr(attrs, h.prefix, a)
		return true
	})
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	return Line{Time: t.UTC(), Level: r.Level.String(), Msg: r.Message, Attrs: attrs}
}

// putAttr flattens a, and any group inside it, into dst under dotted keys.
func putAttr(dst map[string]any, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range v.Group() {
			putAttr(dst, p, g)
		}
		return
	}
	if a.Key == "" {
		return
	}
	dst[prefix+a.Key] = jsonValue(v)
}

// jsonValue renders v the way the JSON log handler would, as a value
// encoding/json can marshal.
func jsonValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().UTC().Format(time.RFC3339Nano)
	default:
		switch x := v.Any().(type) {
		case error:
			return x.Error()
		case fmt.Stringer:
			return x.String()
		case json.Marshaler:
			if raw, err := x.MarshalJSON(); err == nil && json.Valid(raw) {
				return json.RawMessage(raw)
			}
			return fmt.Sprint(x)
		default:
			if raw, err := json.Marshal(x); err == nil {
				return json.RawMessage(raw)
			}
			return fmt.Sprint(x)
		}
	}
}
