package interact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

const VisionUnavailableReason = "vision provider is unavailable"

// visionMaxOutputTokens bounds a description. An image analysis is a paragraph,
// not an essay, and the ceiling is also what keeps one image_analyze call from
// spending a turn's whole output budget.
const visionMaxOutputTokens = 2048

// VisionClient analyzes a workspace image with an optional prompt.
type VisionClient interface {
	Analyze(ctx context.Context, imagePath, prompt string) (string, error)
}

// FuncVision adapts a function to VisionClient.
type FuncVision func(ctx context.Context, imagePath, prompt string) (string, error)

func (f FuncVision) Analyze(ctx context.Context, imagePath, prompt string) (string, error) {
	return f(ctx, imagePath, prompt)
}

// RouteVision analyzes an image by sampling the vision route through the same
// provider every other model call goes through.
//
// It used to assemble its own Chat Completions request and POST it directly,
// which meant the one call in the runtime most likely to be expensive — an image
// is thousands of input tokens — had no retry, no idempotency key, no usage, no
// cost and no span. It also hardcoded a protocol that had nothing to do with the
// route it was given.
type RouteVision struct {
	// Provider is the shared client. When it is the engine's tool sampler, the
	// call lands in the turn's account rather than beside it.
	Provider provider.Provider
	Route    model.ReadyRoute
}

func (r RouteVision) Analyze(ctx context.Context, imagePath, prompt string) (string, error) {
	if r.Provider == nil {
		return "", errors.New(VisionUnavailableReason)
	}
	if err := r.Route.Validate(); err != nil {
		return "", fmt.Errorf("%s: %w", VisionUnavailableReason, err)
	}
	payload, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	if len(payload) == 0 {
		return "", errors.New("image is empty")
	}
	question := strings.TrimSpace(prompt)
	if question == "" {
		question = "Describe this image."
	}
	maxOut := r.Route.Model().Limits.MaxOutputTokens
	if maxOut == 0 || maxOut > visionMaxOutputTokens {
		maxOut = visionMaxOutputTokens
	}
	stream, err := r.Provider.Stream(ctx, provider.ModelRequest{
		Route:   r.Route,
		Purpose: model.PurposeVision,
		Messages: []provider.Message{{
			Role: provider.RoleUser,
			Blocks: []provider.ContentBlock{
				{Type: provider.ContentText, Text: question},
				{Type: provider.ContentImage, Attachment: &provider.Attachment{
					MediaType: imageMediaType(imagePath),
					Data:      payload,
					Name:      filepath.Base(imagePath),
				}},
			},
		}},
		MaxOutputTokens: maxOut,
		// Describing an image has no side effects and the body is identical on a
		// second attempt, so a retry is safe and gets the same idempotency key
		// the engine's own samples get.
		Idempotent: true,
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	var text strings.Builder
	for {
		event, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return "", recvErr
		}
		if event.Type == provider.EventTextDelta {
			text.WriteString(event.Text)
		}
	}
	if text.Len() == 0 {
		return "", errors.New("vision model returned no description")
	}
	return text.String(), nil
}

// imageMediaType is what the file's extension claims it is. A file with no
// recognisable extension is sent as PNG, which is what the previous
// implementation did and what a screenshot almost always is.
func imageMediaType(path string) string {
	media := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if media == "" || !strings.HasPrefix(media, "image/") {
		return "image/png"
	}
	// A registered type can carry parameters ("image/svg+xml; charset=utf-8");
	// providers want the bare type.
	if index := strings.IndexByte(media, ';'); index >= 0 {
		media = strings.TrimSpace(media[:index])
	}
	return media
}
