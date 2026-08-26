package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestImageReopenUsesHandleWithoutTextualImageBytes(t *testing.T) {
	registry := NewRegistry(nil, nil)
	if err := RegisterImageReopen(registry); err != nil {
		t.Fatal(err)
	}
	messages := []provider.Message{{
		Role: provider.RoleUser,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentImage,
			Attachment: &provider.Attachment{
				Name: "diagram.png", MediaType: "image/png", Data: []byte("image-bytes"),
			},
		}},
	}}
	if err := registry.BindImageHandles(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	handle := messages[0].Blocks[0].Attachment.Handle
	if !strings.HasPrefix(handle, "image_") {
		t.Fatalf("handle = %q", handle)
	}
	result, err := registry.Execute(context.Background(), Call{
		Name:       ImageReopenToolName,
		Arguments:  []byte(`{"handle":"` + handle + `"}`),
		Authorized: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Content, "image-bytes") ||
		len(result.Attachments) != 1 ||
		string(result.Attachments[0].Data) != "image-bytes" {
		t.Fatalf("result = %+v", result)
	}
	projected, err := ProjectModelResults(
		[]provider.ToolCall{{ID: "call-image", Name: ImageReopenToolName}},
		[]Result{result},
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 2 || projected[0].Role != provider.RoleTool ||
		projected[1].Role != provider.RoleUser ||
		projected[1].Blocks[0].Type != provider.ContentImage ||
		string(projected[1].Blocks[0].Attachment.Data) != "image-bytes" {
		t.Fatalf("projected = %+v", projected)
	}
}
