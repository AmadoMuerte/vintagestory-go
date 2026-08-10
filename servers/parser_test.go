package servers

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestParseServers(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`
<aside>irrelevant</aside>
<div class="server">
  <b>12 players</b> on <a href="vintagestoryjoin://example.org:42420">Example &amp; <em>Friends</em></a>
  <img title="7 mods installed">
  <div class="serverdesc">A <strong>friendly</strong> server.</div>
</div>
<div class="server"><b>4 players</b> on <abbr title="Whitelisted players only">Private</abbr><abbr title="Password protected"> </abbr></div>
<div class="server"><b>not a number players</b><abbr>Listed only</abbr></div>
<div class="server"><b>9 players</b></div>`))
	if err != nil {
		t.Fatal(err)
	}

	servers := parseServers(root)
	if len(servers) != 3 {
		t.Fatalf("got %d servers, want 3", len(servers))
	}
	if got := servers[0]; got.Name != "Example & Friends" || got.Address != "example.org:42420" || got.Players != 12 || got.ModCount != 7 || !got.Joinable || got.Description != "A friendly server." {
		t.Fatalf("unexpected public server: %#v", got)
	}
	if got := servers[1]; got.Name != "Private" || !got.RequiresWhitelist || !got.PasswordProtected || got.Joinable {
		t.Fatalf("unexpected restricted server: %#v", got)
	}
	if got := servers[2]; got.Name != "Listed only" || got.Players != 0 || got.Address != "" || got.Joinable {
		t.Fatalf("unexpected incomplete server: %#v", got)
	}
}

func TestParseServersSkipsUnnamedEntries(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`<div class="server"><b>3 players</b></div>`))
	if err != nil {
		t.Fatal(err)
	}
	if servers := parseServers(root); len(servers) != 0 {
		t.Fatalf("got %#v", servers)
	}
}
