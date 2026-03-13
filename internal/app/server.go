package app

import (
	"fmt"
	"net/http"
)

func (a *App) Serve() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			_, _ = w.Write([]byte("POST only"))
			return
		}
		if err := a.RunOnce(); err != nil {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(fmt.Sprintf("run failed: %v", err)))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("run completed"))
	})

	fmt.Printf("[SERVE] listening on %s\n", a.Cfg.Server.Addr)
	return http.ListenAndServe(a.Cfg.Server.Addr, mux)
}
