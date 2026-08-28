package toolsearch

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	commonToolSet = ",tool_search,result_get,handle_read,request_user_input," +
		"update_plan,submit_plan,turn_complete,"
	readToolSet = ",search_text,search_files,search_definition," +
		"search_references,file_read,file_list,file_write,file_edit," +
		"file_apply,shell_read,exec_command,write_stdin,quality_test," +
		"quality_verify,quality_process_smoke,project_map,"
	writeToolSet     = ",search_related_tests,quality_diagnostics,"
	maxRelevantTools = 4
)

type ProjectionRequest struct {
	Catalog        tool.CatalogSnapshot
	Prompt         string
	Intent         protocol.TurnIntent
	MaxDefinitions int
	MaxSchemaBytes int
	Enabled        func(tool.CatalogEntrySnapshot) bool
}

func ProjectDefinitions(
	request ProjectionRequest,
) ([]provider.ToolDefinition, map[string]bool, error) {
	var descriptors []tool.Descriptor
	var entries []tool.CatalogEntrySnapshot
	for _, entry := range request.Catalog.Entries() {
		presentation := entry.PresentationDescriptor()
		enabled := request.Enabled == nil || request.Enabled(entry)
		if presentation.Visibility == tool.VisibleModel &&
			entry.Descriptor.Availability != tool.AvailabilityUnavailable &&
			enabled {
			entries = append(entries, entry)
			descriptors = append(descriptors, presentation)
		}
	}
	if onlyRetrievalHelpers(descriptors) {
		return nil, map[string]bool{}, nil
	}
	selected := make(map[string]bool)
	var search *tool.CatalogEntrySnapshot
	relevant := 0
	for index := range entries {
		entry := entries[index]
		if entry.Name == ToolName {
			search = &entry
			continue
		}
		if entry.State == tool.CatalogEntryDeferred {
			continue
		}
		if coreTool(request.Intent, entry.Name) ||
			entry.State == tool.CatalogEntryMaterialized ||
			requiredAgentTool(request.Prompt, entry.Name) ||
			entry.Name == "image_analyze" &&
				strings.Contains(strings.ToLower(request.Prompt), "screenshot") {
			selected[entry.Name] = true
			continue
		}
		if relevant < maxRelevantTools &&
			ScoreDescriptor(entry.PresentationDescriptor(), request.Prompt) > 0 {
			selected[entry.Name] = true
			relevant++
		}
	}
	if search == nil {
		for _, entry := range entries {
			if entry.State != tool.CatalogEntryDeferred {
				selected[entry.Name] = true
			}
		}
	} else if len(selected) < len(entries)-1 {
		selected[ToolName] = true
	}
	result := make([]provider.ToolDefinition, 0, len(descriptors))
	advertised := make(map[string]bool)
	schemaBytes := 0
	add := func(entry tool.CatalogEntrySnapshot, required bool) error {
		descriptor := entry.PresentationDescriptor()
		data, _ := json.Marshal(descriptor.InputSchema)
		if len(result)+1 > request.MaxDefinitions ||
			schemaBytes+len(data) > request.MaxSchemaBytes {
			if required {
				return fmt.Errorf(
					"%w: provider tools[] cannot fit required tool %q",
					tool.ErrCatalogLimit,
					descriptor.Name,
				)
			}
			return nil
		}
		result = append(result, provider.ToolDefinition{
			Name: descriptor.Name, Description: descriptor.Description,
			InputSchema: descriptor.InputSchema,
		})
		advertised[descriptor.Name] = true
		schemaBytes += len(data)
		return nil
	}
	if search != nil && selected[ToolName] {
		if err := add(*search, true); err != nil {
			return nil, nil, err
		}
	}
	for _, entry := range entries {
		if !selected[entry.Name] || entry.Name == ToolName {
			continue
		}
		required := coreTool(request.Intent, entry.Name) ||
			entry.State == tool.CatalogEntryMaterialized ||
			requiredAgentTool(request.Prompt, entry.Name)
		if err := add(entry, required); err != nil {
			return nil, nil, err
		}
	}
	return result, advertised, nil
}

func coreTool(intent protocol.TurnIntent, name string) bool {
	in := func(set string) bool { return strings.Contains(set, ","+name+",") }
	if in(commonToolSet) || in(readToolSet) {
		return true
	}
	switch protocol.NormalizeTurnIntent(intent) {
	case protocol.TurnIntentWorkspaceChange, protocol.TurnIntentOperation:
		return in(writeToolSet)
	default:
		return false
	}
}

func requiredAgentTool(prompt, name string) bool {
	lower := strings.ToLower(prompt)
	if strings.Contains(lower, name) {
		return true
	}
	hasAgentSubject := strings.Contains(lower, "agent") ||
		strings.Contains(lower, "child") ||
		strings.Contains(lower, "explorer") ||
		strings.Contains(lower, "implementer")
	switch name {
	case "spawn_agent":
		return hasAgentSubject &&
			(strings.Contains(lower, "spawn ") ||
				strings.Contains(lower, "delegat"))
	case "wait_agent":
		return hasAgentSubject && strings.Contains(lower, "wait for")
	case "integrate_agent":
		return hasAgentSubject && strings.Contains(lower, "integrat")
	default:
		return false
	}
}

func onlyRetrievalHelpers(descriptors []tool.Descriptor) bool {
	if len(descriptors) == 0 {
		return false
	}
	for _, descriptor := range descriptors {
		switch descriptor.Name {
		case "result_get", "handle_read":
		default:
			return false
		}
	}
	return true
}
