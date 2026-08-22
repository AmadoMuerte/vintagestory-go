package servers

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var (
	playersPattern        = regexp.MustCompile(`^(\d+)\s+players?$`)
	currentPlayersPattern = regexp.MustCompile(`^(\d+)\s*/\s*\d+$`)
	modsPattern           = regexp.MustCompile(`^(\d+)\s+mods?\s+installed$`)
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
				if matches := currentPlayersPattern.FindStringSubmatch(nodeText(current)); len(matches) == 2 {
					server.Players, _ = strconv.Atoi(matches[1])
				}
			}
		case "a":
			href := attribute(current, "href")
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
			if matches := modsPattern.FindStringSubmatch(attribute(current, "title")); len(matches) == 2 {
				server.ModCount, _ = strconv.Atoi(matches[1])
			}
		case "i":
			if currentCatalog {
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
