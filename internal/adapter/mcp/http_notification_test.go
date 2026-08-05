package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReadSSEResponseDispatchesCatalogNotification(t *testing.T) {
	stream := strings.NewReader(
		"event: message\n" +
			"data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n" +
			"event: message\n" +
			"data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"tools\":[]}}\n\n",
	)
	var notifications []Notification
	response, err := readSSEResponse(
		stream, json.RawMessage("7"), 4096, 2048,
		func(notification Notification) {
			notifications = append(notifications, notification)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 ||
		notifications[0].Method != "notifications/tools/list_changed" {
		t.Fatalf("notifications = %+v", notifications)
	}
	if string(response.ID) != "7" {
		t.Fatalf("response ID = %s", response.ID)
	}
}
