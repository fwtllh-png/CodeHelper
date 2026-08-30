package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type IDEQuery struct {
	Path         string
	Line         int
	Character    int
	EndLine      int
	EndCharacter int
	NewName      string
}

type IDEResult struct {
	Method string          `json:"method"`
	Server string          `json:"server"`
	Result json.RawMessage `json:"result"`
}

func (c Checker) Hover(ctx context.Context, query IDEQuery) (IDEResult, error) {
	return c.documentRequest(ctx, "textDocument/hover", query)
}

func (c Checker) Formatting(ctx context.Context, query IDEQuery) (IDEResult, error) {
	return c.documentRequest(ctx, "textDocument/formatting", query)
}

func (c Checker) CodeActions(ctx context.Context, query IDEQuery) (IDEResult, error) {
	return c.documentRequest(ctx, "textDocument/codeAction", query)
}

func (c Checker) Rename(ctx context.Context, query IDEQuery) (IDEResult, error) {
	if strings.TrimSpace(query.NewName) == "" {
		return IDEResult{}, errors.New("rename requires a non-empty new name")
	}
	return c.documentRequest(ctx, "textDocument/rename", query)
}

func (c Checker) documentRequest(
	ctx context.Context,
	method string,
	query IDEQuery,
) (IDEResult, error) {
	if strings.TrimSpace(query.Path) == "" {
		return IDEResult{}, errors.New("language server request requires a relative path")
	}
	if method != "textDocument/formatting" &&
		(query.Line < 1 || query.Character < 1) {
		return IDEResult{}, errors.New(
			"language server request requires 1-based line and character",
		)
	}
	resolved, err := c.forPaths([]string{query.Path})
	if err != nil {
		return IDEResult{}, err
	}
	c = resolved
	client, err := c.start(ctx)
	if err != nil {
		return IDEResult{}, err
	}
	defer client.close()

	var initialized struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := client.call(ctx, "initialize", map[string]any{
		"processId": nil,
		"rootUri":   pathURI(client.root),
		"capabilities": map[string]any{"textDocument": map[string]any{
			"hover":      map[string]any{},
			"formatting": map[string]any{},
			"codeAction": map[string]any{},
			"rename":     map[string]any{"prepareSupport": true},
		}},
	}, &initialized, nil); err != nil {
		return IDEResult{}, fmt.Errorf("initialize language server: %w", err)
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		return IDEResult{}, err
	}
	path, text, err := semanticDocument(client.root, query.Path)
	if err != nil {
		return IDEResult{}, err
	}
	uri := pathURI(path)
	if err := client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri": uri, "languageId": languageID(path), "version": 1, "text": text,
		},
	}); err != nil {
		return IDEResult{}, err
	}
	params := map[string]any{"textDocument": map[string]any{"uri": uri}}
	position := map[string]any{
		"line": query.Line - 1, "character": query.Character - 1,
	}
	switch method {
	case "textDocument/formatting":
		params["options"] = map[string]any{
			"tabSize": 4, "insertSpaces": true, "trimTrailingWhitespace": true,
			"insertFinalNewline": true, "trimFinalNewlines": true,
		}
	case "textDocument/codeAction":
		endLine, endCharacter := query.EndLine, query.EndCharacter
		if endLine < 1 {
			endLine, endCharacter = query.Line, query.Character
		}
		params["range"] = map[string]any{
			"start": position,
			"end": map[string]any{
				"line": endLine - 1, "character": max(endCharacter-1, 0),
			},
		}
		params["context"] = map[string]any{"diagnostics": []any{}}
	default:
		params["position"] = position
	}
	if method == "textDocument/rename" {
		params["newName"] = query.NewName
	}
	var raw json.RawMessage
	if err := client.call(ctx, method, params, &raw, nil); err != nil {
		return IDEResult{}, err
	}
	if err := client.call(ctx, "shutdown", nil, nil, nil); err != nil {
		return IDEResult{}, err
	}
	if err := client.notify("exit", nil); err != nil {
		return IDEResult{}, err
	}
	client.finish(500 * time.Millisecond)
	server := strings.TrimSpace(initialized.ServerInfo.Name)
	if server == "" {
		server = filepath.Base(c.Binary)
	}
	return IDEResult{Method: method, Server: server, Result: raw}, nil
}
