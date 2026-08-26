package extension

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestTurnImageAttachmentsPreserveValidatedModelInput(t *testing.T) {
	images := turnImageAttachments([]provider.Attachment{{
		Name: "lake.png", MediaType: "image/png", Data: []byte("image"),
	}})
	if len(images) != 1 ||
		images[0].Label != "lake.png" ||
		images[0].MediaType != "image/png" ||
		images[0].Content != "aW1hZ2U=" {
		t.Fatalf("turn images = %+v", images)
	}
}
