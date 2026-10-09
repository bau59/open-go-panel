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

func TestManagedProxyStaticEditing(t *testing.T) {
 settings := defaultGlobalSettings()
 original, err := ProxyStaticTemplateCache("/home/hq4/apps/hq4/public", "/assets/*\n/site.webmanifest", settings, 86400)
 if err != nil { t.Fatal(err) }
 if !IsManagedProxyStatic(original) { t.Fatal("generated template was not recognized") }
 if got := ManagedProxyStaticPaths(original); got != "/assets/*\n/site.webmanifest" { t.Fatalf("unexpected paths: %q", got) }
 if got := ManagedProxyStaticCache(original); got != 86400 { t.Fatalf("unexpected cache: %d", got) }
 updated, err := ProxyStaticTemplateCache("/home/hq4/apps/hq4/public", "/assets/*", settings, 0)
 if err != nil { t.Fatal(err) }
 if !strings.Contains(updated, "Cache-Control \"no-cache\"") { t.Fatal("missing revalidation header") }
 if IsManagedProxyStatic("{domain} { reverse_proxy 127.0.0.1:{port} }") { t.Fatal("manual config recognized as managed") }
}
