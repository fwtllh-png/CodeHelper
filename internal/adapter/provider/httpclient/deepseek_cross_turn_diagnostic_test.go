package httpclient

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestDeepSeekCrossTurnCacheDiagnostic(t *testing.T) {
	if os.Getenv(deepSeekLiveControlEnv) != "1" {
		t.Skipf("DeepSeek live control disabled; set %s=1", deepSeekLiveControlEnv)
	}
	for _, protocol := range []model.WireProtocol{
		model.ProtocolOpenAIResponses,
		model.ProtocolOpenAIChat,
	} {
		t.Run(string(protocol), func(t *testing.T) {
			runtime, route, _ := deepSeekLiveRuntimeForProtocol(t, protocol)
			nonce := fmt.Sprintf(
				"codehelper-cross-turn-%s-%d ",
				protocol,
				time.Now().UnixNano(),
			)
			common := nonce + strings.Repeat(
				"Stable CodeHelper repository context preserved across user turns. ",
				23_000,
			)
			tails := []string{
				strings.Repeat("Turn one transient analysis evidence alpha. ", 5_500),
				strings.Repeat("Turn two transient implementation evidence beta. ", 5_500),
				strings.Repeat("Turn three transient verification evidence gamma. ", 5_500),
			}
			for index, tail := range tails {
				usage := deepSeekDiagnosticUsage(t, runtime, provider.ModelRequest{
					Route: route,
					Messages: []provider.Message{
						provider.TextMessage(provider.RoleSystem, common),
						provider.TextMessage(provider.RoleUser,
							tail+"\nReply with exactly ok."),
					},
					MaxOutputTokens: 32, ReasoningEffort: "low", Idempotent: true,
				})
				t.Logf("cross sample=%d input=%d cached=%d share=%.2f%%",
					index+1, usage.InputTokens, usage.CachedTokens,
					float64(usage.CachedTokens)*100/float64(usage.InputTokens))
			}
			commonUsage := deepSeekDiagnosticUsage(t, runtime, provider.ModelRequest{
				Route: route,
				Messages: []provider.Message{
					provider.TextMessage(provider.RoleSystem, common),
					provider.TextMessage(provider.RoleUser, "Reply with exactly ok."),
				},
				MaxOutputTokens: 32, ReasoningEffort: "low", Idempotent: true,
			})
			t.Logf("common-only input=%d cached=%d share=%.2f%%",
				commonUsage.InputTokens, commonUsage.CachedTokens,
				float64(commonUsage.CachedTokens)*100/float64(commonUsage.InputTokens))
		})
	}
}

func TestDeepSeekChatAppendOnlyCache(t *testing.T) {
	if os.Getenv(deepSeekLiveControlEnv) != "1" {
		t.Skipf(
			"DeepSeek live diagnostic disabled; set %s=1",
			deepSeekLiveControlEnv,
		)
	}
	runtime, route, _ := deepSeekLiveRuntime(t)
	if route.Protocol() != model.ProtocolOpenAIChat {
		t.Fatalf("default DeepSeek protocol = %q", route.Protocol())
	}
	nonce := fmt.Sprintf("codehelper-append-only-%d ", time.Now().UnixNano())
	messages := []provider.Message{
		provider.TextMessage(
			provider.RoleSystem,
			nonce+strings.Repeat(
				"Stable append-only repository context. ",
				12_000,
			),
		),
		provider.TextMessage(
			provider.RoleUser,
			"Turn one. Reply with exactly alpha.",
		),
	}
	first := deepSeekDiagnosticUsage(t, runtime, provider.ModelRequest{
		Route: route, Messages: append([]provider.Message(nil), messages...),
		MaxOutputTokens: 32, ReasoningEffort: "low", Idempotent: true,
	})
	messages = append(
		messages,
		provider.Message{
			Role: provider.RoleAssistant,
			Blocks: []provider.ContentBlock{
				{Type: provider.ContentReasoning, Text: "Return the requested token."},
				{Type: provider.ContentText, Text: "alpha"},
			},
		},
		provider.TextMessage(
			provider.RoleUser,
			"Turn two. Reply with exactly beta.",
		),
	)
	second := deepSeekDiagnosticUsage(t, runtime, provider.ModelRequest{
		Route: route, Messages: append([]provider.Message(nil), messages...),
		MaxOutputTokens: 32, ReasoningEffort: "low", Idempotent: true,
	})
	messages = append(
		messages,
		provider.TextMessage(provider.RoleAssistant, "beta"),
		provider.TextMessage(
			provider.RoleUser,
			"Turn three. Reply with exactly gamma.",
		),
	)
	third := deepSeekDiagnosticUsage(t, runtime, provider.ModelRequest{
		Route: route, Messages: append([]provider.Message(nil), messages...),
		MaxOutputTokens: 32, ReasoningEffort: "low", Idempotent: true,
	})
	t.Logf(
		"append-only input/cached: first=%d/%d second=%d/%d third=%d/%d",
		first.InputTokens,
		first.CachedTokens,
		second.InputTokens,
		second.CachedTokens,
		third.InputTokens,
		third.CachedTokens,
	)
	if second.CachedTokens <= first.CachedTokens {
		t.Fatalf(
			"second Turn cache did not improve: first=%+v second=%+v",
			first,
			second,
		)
	}
	if third.CachedTokens < second.CachedTokens {
		t.Fatalf(
			"third Turn cache regressed: second=%+v third=%+v",
			second,
			third,
		)
	}
}

func deepSeekDiagnosticUsage(
	t *testing.T,
	runtime provider.Provider,
	request provider.ModelRequest,
) provider.Usage {
	t.Helper()
	stream, err := runtime.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	var usage provider.Usage
	for _, event := range events {
		if event.Type == provider.EventUsage && event.Usage != nil {
			usage = *event.Usage
		}
	}
	if usage.InputTokens == 0 {
		t.Fatal("DeepSeek diagnostic returned no input usage")
	}
	return usage
}
