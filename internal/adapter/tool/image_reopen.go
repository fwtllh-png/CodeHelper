package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
)

const ImageReopenToolName = "image_reopen"

type storedImage struct {
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"`
	Name      string `json:"name,omitempty"`
}

type ImageStore struct{ content contentstore.Store }

func newImageStore(content contentstore.Store) *ImageStore {
	return &ImageStore{content: content}
}

func imageRecord(attachment provider.Attachment) ([]byte, error) {
	return json.Marshal(storedImage{
		MediaType: attachment.MediaType,
		Data:      attachment.Data,
		Name:      attachment.Name,
	})
}

func (s *ImageStore) Put(
	ctx context.Context,
	attachment provider.Attachment,
) (string, error) {
	if s == nil || s.content == nil {
		return "", errors.New("image content store is unavailable")
	}
	record, err := imageRecord(attachment)
	if err != nil {
		return "", err
	}
	handle := contentstore.StableHandle("image", record)
	if _, err = s.content.Get(ctx, handle); err == nil {
		return handle, nil
	} else if !errors.Is(err, contentstore.ErrNotFound) {
		return "", err
	}
	if err := s.content.Put(ctx, handle, record); err != nil {
		return "", err
	}
	return handle, nil
}

func (s *ImageStore) Get(
	ctx context.Context,
	handle string,
) (provider.Attachment, error) {
	if s == nil || s.content == nil {
		return provider.Attachment{}, errors.New("image content store is unavailable")
	}
	record, err := s.content.Get(ctx, handle)
	if err != nil {
		return provider.Attachment{}, err
	}
	var stored storedImage
	if err := json.Unmarshal(record, &stored); err != nil {
		return provider.Attachment{}, err
	}
	return provider.Attachment{
		MediaType: stored.MediaType,
		Data:      stored.Data,
		Name:      stored.Name,
		Handle:    handle,
	}, nil
}

func (r *Registry) BindImageHandles(
	ctx context.Context,
	messages []provider.Message,
) error {
	for messageIndex := range messages {
		for blockIndex := range messages[messageIndex].Blocks {
			block := &messages[messageIndex].Blocks[blockIndex]
			if block.Type != provider.ContentImage || block.Attachment == nil {
				continue
			}
			handle, err := r.images.Put(ctx, *block.Attachment)
			if err != nil {
				return err
			}
			if block.Attachment.Handle != "" &&
				block.Attachment.Handle != handle {
				return errors.New("image attachment handle does not match its content")
			}
			block.Attachment.Handle = handle
		}
	}
	return nil
}

type imageReopen struct{ store *ImageStore }

func RegisterImageReopen(registry *Registry) error {
	if registry == nil || registry.images == nil {
		return errors.New("image_reopen registry is required")
	}
	return registry.Register(&imageReopen{store: registry.images})
}

func (*imageReopen) Descriptor() Descriptor {
	return Descriptor{
		Name: ImageReopenToolName,
		Description: "Reopen one historical image using the exact image_* handle " +
			"from an omitted-image notice. The runtime injects the original image " +
			"into the next model context without copying its bytes into tool text.",
		Visibility: VisibleModel,
		Capability: CapabilityRead, AccessMode: AccessRead,
		ParallelPolicy:     ParallelConcurrent,
		RepeatPolicy:       RepeatExecute,
		SandboxRequirement: SandboxNone,
		Availability:       AvailabilityAvailable,
		ResourceResolver: ResourceResolver{Templates: []ResourceTemplate{{
			Kind: "image_handle", Field: "handle", Access: AccessRead,
		}}},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"handle": map[string]any{
					"type": "string", "pattern": "^image_[a-f0-9]{64}$",
				},
			},
			"required": []string{"handle"}, "additionalProperties": false,
		},
	}
}

func (t *imageReopen) Execute(
	ctx context.Context,
	raw json.RawMessage,
) (Result, error) {
	var input struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return Result{}, err
	}
	attachment, err := t.store.Get(ctx, input.Handle)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Content: fmt.Sprintf(
			"historical image reopened: name=%q media_type=%q handle=%s",
			attachment.Name, attachment.MediaType, attachment.Handle,
		),
		Attachments: []provider.Attachment{attachment},
	}, nil
}

func (*imageReopen) ExecutionDisposition() ExecutionDisposition {
	return DispositionAbortImmediately
}

func (t *imageReopen) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (Result, Outcome, error) {
	return ExecuteWithOutcome(ctx, t, raw)
}
