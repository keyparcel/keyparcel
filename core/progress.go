package core

import (
	"context"
	"io"
	"time"
)

// Progress measures encrypted parcel bytes, not successful delivery. Total <= 0
// means an indeterminate phase. Item/Items identify a recipient within a batch;
// byte percentages restart for each recipient and never summarize confirmations.
type Progress struct {
	Phase        string
	Bytes, Total int64
	Item, Items  int
}
type progressKey struct{}

// WithProgress attaches a synchronous, lightweight observer to one request.
// Observers must not block or start network operations. Canceled contexts emit no
// further updates; GUI observers must also reject stale queued presentation.
func WithProgress(ctx context.Context, observe func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, observe)
}
func reportProgress(ctx context.Context, p Progress) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	if observe, ok := ctx.Value(progressKey{}).(func(Progress)); ok && observe != nil {
		observe(p)
	}
}
func phase(ctx context.Context, name string) { reportProgress(ctx, Progress{Phase: name}) }

type progressCounter struct {
	ctx   context.Context
	value Progress
	last  time.Time
	sent  int64
}

func newProgressCounter(ctx context.Context, p Progress) *progressCounter {
	reportProgress(ctx, p)
	return &progressCounter{ctx: ctx, value: p, last: time.Now()}
}

type progressReader struct {
	input io.Reader
	*progressCounter
}

func newProgressReader(ctx context.Context, r io.Reader, p Progress) *progressReader {
	return &progressReader{input: r, progressCounter: newProgressCounter(ctx, p)}
}
func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.input.Read(b)
	r.advance(n, err)
	return n, err
}

// Count upload bytes only after the destination accepted them, including partial
// writes; reading ahead from the local package must not imply upload success.
type progressWriter struct {
	output   io.Writer
	progress *progressCounter
}

func newProgressWriter(ctx context.Context, w io.Writer, p Progress) *progressWriter {
	return &progressWriter{output: w, progress: newProgressCounter(ctx, p)}
}
func (w *progressWriter) Write(b []byte) (int, error) {
	n, err := w.output.Write(b)
	w.progress.advance(n, err)
	return n, err
}

func (r *progressCounter) advance(n int, err error) {
	before := r.value.Bytes
	r.value.Bytes += int64(n)
	crossed := r.value.Total > 0 && before < r.value.Total && r.value.Bytes >= r.value.Total
	if r.value.Bytes != r.sent && (time.Since(r.last) >= 100*time.Millisecond || err != nil || crossed) {
		reportProgress(r.ctx, r.value)
		r.last = time.Now()
		r.sent = r.value.Bytes
	}
}
