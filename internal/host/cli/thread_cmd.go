package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newThreadCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "thread", Short: "Inspect and manage local thread metadata"}
	list := &cobra.Command{
		Use: "list", Short: "List threads under a data directory",
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			dataDir, _ := cmd.Flags().GetString("data-dir")
			if dataDir == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread list --data-dir is required")
				setCode(2)
				return
			}
			names, err := listThreadDirs(dataDir)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread list: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"threads": names, "data_dir": dataDir})
			} else {
				for _, name := range names {
					_, _ = fmt.Fprintln(stdout, name)
				}
			}
			setCode(0)
		},
	}
	list.Flags().Bool("json", false, "emit JSON")
	list.Flags().String("data-dir", "", "persistent state directory")

	resume := &cobra.Command{
		Use: "resume", Short: "Mark a thread as active for subsequent sessions",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if dataDir == "" || id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread resume requires --data-dir and --id")
				setCode(2)
				return
			}
			threadDir := filepath.Join(dataDir, normalizeThreadID(id))
			info, err := os.Stat(threadDir)
			if err != nil || !info.IsDir() {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread resume: thread %q not found under %s\n", id, dataDir)
				setCode(1)
				return
			}
			active := normalizeThreadID(id)
			if err := os.WriteFile(filepath.Join(dataDir, "active-thread"), []byte(active+"\n"), 0o600); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread resume: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"active_thread": active, "data_dir": dataDir,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "active_thread=%s\n", active)
			}
			setCode(0)
		},
	}
	resume.Flags().String("data-dir", "", "persistent state directory")
	resume.Flags().String("id", "", "thread id or thread-* directory name")
	resume.Flags().Bool("json", false, "emit JSON")

	fork := &cobra.Command{
		Use: "fork", Short: "Copy a thread directory to a new thread id",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			from, _ := cmd.Flags().GetString("from")
			to, _ := cmd.Flags().GetString("to")
			asJSON, _ := cmd.Flags().GetBool("json")
			if dataDir == "" || from == "" || to == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread fork requires --data-dir, --from, and --to")
				setCode(2)
				return
			}
			srcID := normalizeThreadID(from)
			dstID := normalizeThreadID(to)
			src := filepath.Join(dataDir, srcID)
			dst := filepath.Join(dataDir, dstID)
			if srcID == dstID {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread fork: --from and --to must differ")
				setCode(2)
				return
			}
			info, err := os.Stat(src)
			if err != nil || !info.IsDir() {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread fork: source %q not found\n", from)
				setCode(1)
				return
			}
			if _, err := os.Stat(dst); err == nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread fork: destination %q already exists\n", to)
				setCode(1)
				return
			}
			if err := copyDir(src, dst); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread fork: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"from": srcID, "to": dstID, "data_dir": dataDir,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "forked %s -> %s\n", srcID, dstID)
			}
			setCode(0)
		},
	}
	fork.Flags().String("data-dir", "", "persistent state directory")
	fork.Flags().String("from", "", "source thread id")
	fork.Flags().String("to", "", "destination thread id")
	fork.Flags().Bool("json", false, "emit JSON")

	archive := &cobra.Command{
		Use: "archive", Short: "Move a thread directory under archived/",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if dataDir == "" || id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread archive requires --data-dir and --id")
				setCode(2)
				return
			}
			srcID := normalizeThreadID(id)
			src := filepath.Join(dataDir, srcID)
			info, err := os.Stat(src)
			if err != nil || !info.IsDir() {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread archive: thread %q not found\n", id)
				setCode(1)
				return
			}
			archiveRoot := filepath.Join(dataDir, "archived")
			if err := os.MkdirAll(archiveRoot, 0o700); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread archive: %v\n", err)
				setCode(1)
				return
			}
			dst := filepath.Join(archiveRoot, srcID)
			if _, err := os.Stat(dst); err == nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread archive: %s already archived\n", srcID)
				setCode(1)
				return
			}
			if err := os.Rename(src, dst); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread archive: %v\n", err)
				setCode(1)
				return
			}
			activePath := filepath.Join(dataDir, "active-thread")
			if data, err := os.ReadFile(activePath); err == nil && strings.TrimSpace(string(data)) == srcID {
				_ = os.Remove(activePath)
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"archived": srcID, "path": dst, "data_dir": dataDir,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "archived %s -> %s\n", srcID, dst)
			}
			setCode(0)
		},
	}
	archive.Flags().String("data-dir", "", "persistent state directory")
	archive.Flags().String("id", "", "thread id")
	archive.Flags().Bool("json", false, "emit JSON")

	rename := &cobra.Command{
		Use: "rename", Short: "Rename a thread directory",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			from, _ := cmd.Flags().GetString("from")
			to, _ := cmd.Flags().GetString("to")
			asJSON, _ := cmd.Flags().GetBool("json")
			if dataDir == "" || from == "" || to == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread rename requires --data-dir, --from, and --to")
				setCode(2)
				return
			}
			srcID := normalizeThreadID(from)
			dstID := normalizeThreadID(to)
			src := filepath.Join(dataDir, srcID)
			dst := filepath.Join(dataDir, dstID)
			if srcID == dstID {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread rename: --from and --to must differ")
				setCode(2)
				return
			}
			info, err := os.Stat(src)
			if err != nil || !info.IsDir() {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread rename: source %q not found\n", from)
				setCode(1)
				return
			}
			if _, err := os.Stat(dst); err == nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread rename: destination %q already exists\n", to)
				setCode(1)
				return
			}
			if err := os.Rename(src, dst); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread rename: %v\n", err)
				setCode(1)
				return
			}
			activePath := filepath.Join(dataDir, "active-thread")
			if data, err := os.ReadFile(activePath); err == nil && strings.TrimSpace(string(data)) == srcID {
				_ = os.WriteFile(activePath, []byte(dstID+"\n"), 0o600)
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"from": srcID, "to": dstID, "data_dir": dataDir,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "renamed %s -> %s\n", srcID, dstID)
			}
			setCode(0)
		},
	}
	rename.Flags().String("data-dir", "", "persistent state directory")
	rename.Flags().String("from", "", "source thread id")
	rename.Flags().String("to", "", "destination thread id")
	rename.Flags().Bool("json", false, "emit JSON")

	readCmd := &cobra.Command{
		Use: "read", Short: "Read thread metadata and file listing",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if dataDir == "" || id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: thread read requires --data-dir and --id")
				setCode(2)
				return
			}
			threadID := normalizeThreadID(id)
			dir := filepath.Join(dataDir, threadID)
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread read: thread %q not found\n", id)
				setCode(1)
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: thread read: %v\n", err)
				setCode(1)
				return
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			sort.Strings(names)
			meta := map[string]any{}
			metaPath := filepath.Join(dir, "meta.json")
			if data, err := os.ReadFile(metaPath); err == nil {
				_ = json.Unmarshal(data, &meta)
			}
			payload := map[string]any{
				"id": threadID, "data_dir": dataDir, "files": names, "meta": meta,
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintf(stdout, "id=%s files=%d\n", threadID, len(names))
				for _, name := range names {
					_, _ = fmt.Fprintln(stdout, name)
				}
			}
			setCode(0)
		},
	}
	readCmd.Flags().String("data-dir", "", "persistent state directory")
	readCmd.Flags().String("id", "", "thread id")
	readCmd.Flags().Bool("json", false, "emit JSON")

	cmd.AddCommand(list, resume, fork, archive, rename, readCmd)
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: thread requires a subcommand (list|resume|fork|archive|rename|read)")
		setCode(2)
	}
	return cmd
}
