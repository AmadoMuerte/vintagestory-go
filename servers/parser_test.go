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

func TestParseServersCurrentCatalog(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`
<li class="section featured" data-sid="42">
  <a class="info" href="/s/42">
    <div class="labels"><span><i class="fa-solid fa-user"></i> 17 / 40</span><span>1.22.7</span></div>
    <div class="summary">A <strong>friendly</strong> current server.</div>
    <h3>Current &amp; Friends</h3>
  </a>
  <a class="join button" href="vintagestoryjoin://current.example:42420">
    <span>Join</span>
    <i class="fa-solid fa-scroll" title="Whitelist required"></i>
    <i class="fa-solid fa-lock" title="Password required"></i>
  </a>
</li>
<li class="section" data-sid="43">
  <a class="info"><div class="labels"><span>3 / 10</span></div><h3>Listed only</h3></a>
</li>`))
	if err != nil {
		t.Fatal(err)
	}

	servers := parseServers(root)
	if len(servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(servers))
	}
	if got := servers[0]; got.Name != "Current & Friends" || got.Address != "current.example:42420" || got.Players != 17 || !got.Joinable || !got.RequiresWhitelist || !got.PasswordProtected || got.Description != "A friendly current server." {
		t.Fatalf("unexpected current server: %#v", got)
	}
	if got := servers[1]; got.Name != "Listed only" || got.Players != 3 || got.Joinable {
		t.Fatalf("unexpected current incomplete server: %#v", got)
	}
}

func TestParseServerDetail(t *testing.T) {
	root, err := html.Parse(strings.NewReader(`
<aside><div id="server-info">
  <img alt="Thumbnail" src="/files/thumb.png">
  <span>Players:</span><span>12 / 40</span>
  <span>Game Version:</span><span>1.22.7</span>
  <span>Location:</span><span>United States</span>
  <span>Languages:</span><span><span class="tag" title="English">en</span></span>
  <span>Operated By:</span><a href="/u/operator">Owner</a>
  <a href="vintagestoryjoin://example.test:42420">Join</a>
</div></aside>
<main class="server" data-sid="42">
  <h1>Example Server</h1>
  <img class="banner" alt="Banner" src="/files/banner.png">
  <div class="text-section"><p>Hello <strong>world</strong>.</p></div>
  <ul class="tag-list"><li><a class="external" href="//mods.vintagestory.at/show/mod/1">Example Mod@1.2.3</a></li></ul>
</main>`))
	if err != nil {
		t.Fatal(err)
	}
	server, ok := parseServerDetail(root)
	if !ok || server.ID != "42" || server.Name != "Example Server" || server.Players != 12 || server.MaxPlayers != 40 || server.GameVersion != "1.22.7" || server.Location != "United States" || len(server.Languages) != 1 || server.Languages[0] != "English" || server.Operator != "Owner" || server.Address != "example.test:42420" || server.ImageURL != "/files/thumb.png" || server.BannerURL != "/files/banner.png" || len(server.Mods) != 1 || server.Mods[0].Version != "1.2.3" || server.FullDescription != "Hello world ." {
		t.Fatalf("unexpected detail: %#v", server)
	}
	if !strings.Contains(server.DescriptionHTML, "<strong>world</strong>") {
		t.Fatalf("description HTML lost formatting: %q", server.DescriptionHTML)
	}
}
