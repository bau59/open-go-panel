package caddy

import (
 "strings"
 "testing"
)

func TestProxyStaticTemplate(t *testing.T) {
 cfg, err := ProxyStaticTemplate("/home/hq4/apps/hq4/public", "/assets/*\n/img/*\n/favicon.ico\n", defaultGlobalSettings())
 if err != nil { t.Fatal(err) }
 for _, fragment := range []string{"root * \"/home/hq4/apps/hq4/public\"", "@ogp_static path /assets/* /img/* /favicon.ico", "handle @ogp_static", "file_server", "reverse_proxy 127.0.0.1:{port}", "handle {"} {
  if !strings.Contains(cfg, fragment) { t.Errorf("missing %q in config", fragment) }
 }
}

func TestProxyStaticTemplateRejectsUnsafePaths(t *testing.T) {
 for _, path := range []string{"/", "/*", "/.env", "/private/../.env", "/assets/* /secret/*", "/foo/*bar", "/assets/{foo}", "/assets//foo", "/file\\evil"} {
  if _, err := ProxyStaticTemplate("/home/hq4/public", path, defaultGlobalSettings()); err == nil { t.Errorf("accepted unsafe path %q", path) }
 }
}
