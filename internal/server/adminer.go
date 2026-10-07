package server

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func registerAdminerRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("POST /adminer/install", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Adminer.Install(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /adminer/start", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Adminer.Start(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /adminer/stop", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Adminer.Stop(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /adminer/update", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Adminer.Update(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	target, _ := url.Parse(cfg.Adminer.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		req.Header.Set("X-Forwarded-Prefix", "/db-admin")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		location := resp.Header.Get("Location")
		if strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "/db-admin") {
			resp.Header.Set("Location", "/db-admin"+location)
		}
		return nil
	}

	mux.Handle("GET /db-admin", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/db-admin/", http.StatusSeeOther)
	})))
	mux.Handle("GET /db-admin/{path...}", requireAuth(store, proxy))
	mux.Handle("POST /db-admin/{path...}", requireAuth(store, proxy))
}
