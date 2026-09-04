package servers

import (
	"bytes"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var (
	playersPattern     = regexp.MustCompile(`^(\d+)\s+players?$`)
	playerLimitPattern = regexp.MustCompile(`^(\d+)\s*/\s*(\d+)$`)
	modsPattern        = regexp.MustCompile(`^(\d+)\s+mods?\s+installed$`)
)

func parseServers(root *html.Node) []Server {
	var servers []Server
	visit(root, func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}
		legacyListing := node.Data == "div" && hasClass(node, "server")
		currentListing := node.Data == "li" && hasClass(node, "section") && attribute(node, "data-sid") != ""
		if !legacyListing && !currentListing {
			return
		}
		if server, ok := parseServer(node); ok {
			servers = append(servers, server)
		}
	})
	sort.SliceStable(servers, func(left, right int) bool {
		return servers[left].Players > servers[right].Players
	})
	return servers
}

func parseServer(node *html.Node) (Server, bool) {
	server := Server{}
	currentCatalog := node.Data == "li"
	server.ID = attribute(node, "data-sid")
	visit(node, func(current *html.Node) {
		if current.Type != html.ElementNode {
			return
		}
		switch current.Data {
		case "b":
			if matches := playersPattern.FindStringSubmatch(nodeText(current)); len(matches) == 2 {
				server.Players, _ = strconv.Atoi(matches[1])
			}
		case "span":
			if currentCatalog && current.Parent != nil && hasClass(current.Parent, "labels") {
				text := nodeText(current)
				if matches := playerLimitPattern.FindStringSubmatch(text); len(matches) == 3 {
					server.Players, _ = strconv.Atoi(matches[1])
					server.MaxPlayers, _ = strconv.Atoi(matches[2])
				} else if server.GameVersion == "" && strings.Contains(text, ".") {
					server.GameVersion = text
				}
			}
			if currentCatalog {
				if tag := attribute(current, "title"); strings.HasPrefix(tag, "Physical server location: ") {
					server.Location = strings.TrimSpace(strings.TrimPrefix(tag, "Physical server location: "))
				} else if hasClass(current, "tag") && tag != "" && !strings.Contains(tag, ":") {
					server.Languages = append(server.Languages, tag)
				}
			}
		case "a":
			href := attribute(current, "href")
			if currentCatalog && strings.HasPrefix(href, "/s/") {
				server.URL = href
			}
			if address := strings.TrimPrefix(href, "vintagestoryjoin://"); address != href {
				if !currentCatalog {
					server.Name = nodeText(current)
				}
				server.Address, server.Joinable = address, true
			}
		case "h3":
			if currentCatalog {
				server.Name = nodeText(current)
			}
		case "abbr":
			if server.Name == "" {
				server.Name = nodeText(current)
			}
			switch strings.ToLower(attribute(current, "title")) {
			case "whitelisted players only":
				server.RequiresWhitelist = true
			case "password protected":
				server.PasswordProtected = true
			}
		case "img":
			if currentCatalog && server.ImageURL == "" {
				server.ImageURL = attribute(current, "src")
			}
			if matches := modsPattern.FindStringSubmatch(attribute(current, "title")); len(matches) == 2 {
				server.ModCount, _ = strconv.Atoi(matches[1])
			}
		case "i":
			if currentCatalog {
				server.Modified = server.Modified || current.Parent != nil && hasClass(current.Parent, "modded-marker")
				server.RequiresWhitelist = server.RequiresWhitelist || hasClass(current, "fa-scroll")
				server.PasswordProtected = server.PasswordProtected || hasClass(current, "fa-lock")
			}
		case "div":
			if hasClass(current, "serverdesc") || currentCatalog && hasClass(current, "summary") {
				server.Description = nodeText(current)
			}
		}
	})
	server.Name = strings.TrimSpace(server.Name)
	server.Address = strings.TrimSpace(server.Address)
	return server, server.Name != ""
}

func parseServerDetail(root *html.Node) (Server, bool) {
	server := Server{}
	visit(root, func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}
		switch node.Data {
		case "main":
			server.ID = attribute(node, "data-sid")
		case "h1":
			if server.Name == "" {
				server.Name = nodeText(node)
			}
		case "img":
			switch attribute(node, "alt") {
			case "Thumbnail":
				server.ImageURL = attribute(node, "src")
			case "Banner":
				server.BannerURL = attribute(node, "src")
			}
		case "div":
			if hasClass(node, "text-section") {
				server.FullDescription = nodeText(node)
				var description bytes.Buffer
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					_ = html.Render(&description, child)
				}
				server.DescriptionHTML = description.String()
			}
		case "a":
			href := attribute(node, "href")
			switch {
			case strings.HasPrefix(href, "vintagestoryjoin://"):
				server.Address = strings.TrimPrefix(href, "vintagestoryjoin://")
				server.Joinable = true
			case strings.HasPrefix(href, "/u/"):
				server.OperatorURL = href
				server.Operator = nodeText(node)

			}
			if hasClass(node, "external") {
				mod := Mod{URL: href}
				text := nodeText(node)
				if at := strings.LastIndex(text, "@"); at > 0 {
					mod.Name, mod.Version = text[:at], text[at+1:]
				} else {
					mod.Name = text
				}
				server.Mods = append(server.Mods, mod)
			}
		case "span":
			if value := detailValue(node); value != "" {
				switch nodeText(node) {
				case "Players:":
					matches := playerLimitPattern.FindStringSubmatch(value)
					if len(matches) == 3 {
						server.Players, _ = strconv.Atoi(matches[1])
						server.MaxPlayers, _ = strconv.Atoi(matches[2])
					}
				case "Game Version:":
					server.GameVersion = value
				case "Location:":
					server.Location = value
				}
			}
			if hasClass(node, "tag") && attribute(node, "title") != "" {
				server.Languages = append(server.Languages, attribute(node, "title"))
			}
		}
	})
	server.Name = strings.TrimSpace(server.Name)
	server.Address = strings.TrimSpace(server.Address)
	server.ModCount = len(server.Mods)
	return server, server.Name != ""
}

func detailValue(label *html.Node) string {
	if label.NextSibling == nil {
		return ""
	}
	for node := label.NextSibling; node != nil; node = node.NextSibling {
		if node.Type == html.ElementNode {
			return nodeText(node)
		}
	}
	return ""
}

func hasClass(node *html.Node, class string) bool {
	for value := range strings.FieldsSeq(attribute(node, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

func visit(node *html.Node, fn func(*html.Node)) {
	fn(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		visit(child, fn)
	}
}

func attribute(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func nodeText(node *html.Node) string {
	var values []string
	visit(node, func(current *html.Node) {
		if current.Type == html.TextNode {
			values = append(values, current.Data)
		}
	})
	return strings.Join(strings.Fields(strings.Join(values, " ")), " ")
}
