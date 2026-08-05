package contentstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrUnaddressable rejects a handle that carries no content digest. A durable
// store is keyed by digest, so a handle it cannot derive one from is a caller
// bug rather than something to store under an invented key.
var ErrUnaddressable = errors.New("content handle carries no digest")

// Blobs is the durable, content-addressed store a Durable writes through to.
// internal/persist/state/cas.Store satisfies it.
type Blobs interface {
	Put(ctx context.Context, id string, data []byte) error
	Get(ctx context.Context, id string) ([]byte, error)
	Retain(ctx context.Context, id string) error
	Release(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	References(ctx context.Context, id string) (uint64, error)
	Close(ctx context.Context) error
}

// Durable is a Store whose contents survive the process. It maps the prefixed
// handles the runtime passes around onto the digest those handles already carry,
// so callers keep using StableHandle and get durability for free.
//
// It differs from Memory in one behaviour that matters: Release deletes content
// nobody references any more. Memory can afford to keep unreferenced bytes until
// the LRU wants the room; on disk, keeping them means a directory that only ever
// grows.
type Durable struct {
	blobs    Blobs
	notFound error
}

// NewDurable wraps a blob store. notFoundSentinel is the error the blob store
// returns for a missing id, which Durable translates to ErrNotFound so callers
// keep matching on one sentinel.
func NewDurable(blobs Blobs, notFoundSentinel error) *Durable {
	return &Durable{blobs: blobs, notFound: notFoundSentinel}
}

func (d *Durable) Put(ctx context.Context, handle string, data []byte) error {
	id, err := digestOf(handle)
	if err != nil {
		return err
	}
	return d.translate(d.blobs.Put(ctx, id, data))
}

func (d *Durable) Get(ctx context.Context, handle string) ([]byte, error) {
	id, err := digestOf(handle)
	if err != nil {
		return nil, err
	}
	data, err := d.blobs.Get(ctx, id)
	return data, d.translate(err)
}

func (d *Durable) Retain(ctx context.Context, handle string) error {
	id, err := digestOf(handle)
	if err != nil {
		return err
	}
	return d.translate(d.blobs.Retain(ctx, id))
}

// Release drops one reference and removes content that has none left.
func (d *Durable) Release(ctx context.Context, handle string) error {
	id, err := digestOf(handle)
	if err != nil {
		return err
	}
	if err := d.translate(d.blobs.Release(ctx, id)); err != nil {
		return err
	}
	refs, err := d.blobs.References(ctx, id)
	if err != nil {
		return d.translate(err)
	}
	if refs > 0 {
		return nil
	}
	return d.translate(d.blobs.Delete(ctx, id))
}

func (d *Durable) Delete(ctx context.Context, handle string) error {
	id, err := digestOf(handle)
	if err != nil {
		return err
	}
	return d.translate(d.blobs.Delete(ctx, id))
}

func (d *Durable) Close(ctx context.Context) error {
	return d.blobs.Close(ctx)
}

func (d *Durable) translate(err error) error {
	switch {
	case err == nil:
		return nil
	case d.notFound != nil && errors.Is(err, d.notFound):
		return ErrNotFound
	default:
		return err
	}
}

// digestOf recovers the digest StableHandle put in the handle. A bare digest is
// accepted too, so callers holding an id rather than a handle still work.
func digestOf(handle string) (string, error) {
	candidate := handle
	if index := strings.LastIndex(handle, "_"); index >= 0 {
		candidate = handle[index+1:]
	}
	if !isDigest(candidate) {
		return "", fmt.Errorf("%w: %q", ErrUnaddressable, handle)
	}
	return candidate, nil
}

func isDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		hexadecimal := (character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f')
		if !hexadecimal {
			return false
		}
	}
	return true
}
