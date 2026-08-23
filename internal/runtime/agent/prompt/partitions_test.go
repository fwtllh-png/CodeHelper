package prompt

import (
	"reflect"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestPartitionTextsUsesReceiptToMessageAlignment(t *testing.T) {
	messages := []provider.Message{
		provider.TextMessage(provider.RoleSystem, "base"),
		provider.TextMessage(provider.RoleSystem, "workspace rule"),
		provider.TextMessage(provider.RoleSystem, "coding method"),
	}
	receipts := []Receipt{
		{Kind: PartitionBase, RetainedBytes: 4},
		{Kind: PartitionMode, RetainedBytes: 0},
		{Kind: PartitionRepository, RetainedBytes: 14},
		{Kind: PartitionCodingPolicy, RetainedBytes: 13},
	}
	got := PartitionTexts(
		messages, receipts, PartitionRepository, PartitionCodingPolicy,
	)
	want := []string{"workspace rule", "coding method"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partition texts = %#v, want %#v", got, want)
	}
}
