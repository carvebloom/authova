package integration

import (
	"bytes"

	"golang.org/x/net/html"
)

// Parse semantic link attributes. CSS class additions or attribute ordering do
// not change the provider redirect contract exercised by the integration test.
func identityProviderURL(data []byte) (string, bool) {
	root, e := html.Parse(bytes.NewReader(data))
	if e != nil {
		return "", false
	}
	var walk func(*html.Node) (string, bool)
	walk = func(node *html.Node) (string, bool) {
		if node.Type == html.ElementNode && node.Data == "a" {
			id, href := "", ""
			for _, a := range node.Attr {
				switch a.Key {
				case "id":
					id = a.Val
				case "href":
					href = a.Val
				}
			}
			if id == "identity-provider-link" && href != "" {
				return href, true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if destination, found := walk(child); found {
				return destination, true
			}
		}
		return "", false
	}
	return walk(root)
}
