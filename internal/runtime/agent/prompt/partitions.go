package prompt

import "github.com/fwtllh-png/QCode/internal/adapter/provider"

// PartitionTexts returns retained prompt sections by receipt identity. Receipts
// with no retained bytes have no matching message and do not advance the
// message cursor.
func PartitionTexts(
	messages []provider.Message,
	receipts []Receipt,
	partitions ...string,
) []string {
	selected := make(map[string]struct{}, len(partitions))
	for _, partition := range partitions {
		selected[partition] = struct{}{}
	}
	result := make([]string, 0)
	messageIndex := 0
	for _, receipt := range receipts {
		if receipt.RetainedBytes <= 0 {
			continue
		}
		if messageIndex >= len(messages) {
			break
		}
		message := messages[messageIndex]
		messageIndex++
		if _, ok := selected[receipt.Kind]; ok {
			if text := message.Text(); text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}
