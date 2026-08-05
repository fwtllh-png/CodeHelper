package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/pairing"
	"github.com/fwtllh-png/CodeHelper/internal/host/webui"
	"github.com/spf13/cobra"
)

func newWebCommand(ctx context.Context, stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use: "web", Short: "Serve the embedded CodeHelper control UI",
		Run: func(cmd *cobra.Command, args []string) {
			listen, _ := cmd.Flags().GetString("listen")
			once, _ := cmd.Flags().GetBool("once")
			asJSON, _ := cmd.Flags().GetBool("json")
			mobile, _ := cmd.Flags().GetBool("mobile")
			qr, _ := cmd.Flags().GetBool("qr")
			handler, err := webui.Handler()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: web: %v\n", err)
				setCode(1)
				return
			}
			mux := http.NewServeMux()
			mux.Handle("/ui/", handler)
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, "ok")
			})
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: web: %v\n", err)
				setCode(1)
				return
			}
			base := "http://" + listener.Addr().String()
			uiURL := base + "/ui/"
			payload := map[string]any{"base_url": base, "ui_url": uiURL}
			if mobile || qr {
				card, err := pairing.New(uiURL, mobile, qr)
				if err != nil {
					_ = listener.Close()
					_, _ = fmt.Fprintf(stderr, "codehelper: web pairing: %v\n", err)
					setCode(1)
					return
				}
				payload["mobile"] = card.Mobile
				payload["qr"] = card.QR
				payload["hint"] = card.Hint
				if card.ASCII != "" {
					payload["ascii_qr"] = card.ASCII
				}
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintf(stdout, "ui_url=%s\n", uiURL)
				if mobile || qr {
					_, _ = fmt.Fprintf(stdout, "mobile=%v qr=%v\n", mobile || qr, qr)
				}
				if qr {
					if ascii, _ := payload["ascii_qr"].(string); ascii != "" {
						_, _ = fmt.Fprintln(stdout, ascii)
					}
				}
			}
			if once {
				_ = listener.Close()
				setCode(0)
				return
			}
			server := &http.Server{Handler: mux}
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
			}()
			err = server.Serve(listener)
			if err != nil && err != http.ErrServerClosed {
				_, _ = fmt.Fprintf(stderr, "codehelper: web: %v\n", err)
				setCode(1)
				return
			}
			setCode(0)
		},
	}
	cmd.Flags().String("listen", "127.0.0.1:0", "HTTP listen address")
	cmd.Flags().Bool("once", false, "print UI URL and exit (hermetic)")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().Bool("mobile", false, "emit mobile pairing fields")
	cmd.Flags().Bool("qr", false, "emit ASCII QR for the UI URL")
	return cmd
}
