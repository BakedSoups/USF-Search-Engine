package main

import (
	"bytes"   // needed to convert slice into io.Reader
	"net/url" // used to parse URLs
	"path"
	"strings" // needed to print out the output in a string format

	"golang.org/x/net/html" // gives us html. functions
	//JUST for stop words json parsing
)


func extract(body []byte) ([]string, []string) {
	var words []string
	var hrefs []string

	doc, err := html.Parse(bytes.NewReader(body))

	if err != nil {
		return words, hrefs
	}

	var f func(*html.Node)

	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "style" {
			return
		}
		if n.Type == html.TextNode {
			// Only process text that's not within an anchor tag
			parent := n.Parent
			isInAnchor := false
			for parent != nil {
				if parent.Data == "a" {
					isInAnchor = true
					break
				}
				parent = parent.Parent
			}
			// skip styling nodes
			if !isInAnchor {
				// lower case all values extracted
				text := strings.ToLower(strings.TrimSpace(n.Data))

				if text != "" {
					textWords := strings.Fields(text)
					for _, word := range textWords {
						words = append(words, word)
					}
				}
			}
		} else if n.Type == html.ElementNode && n.Data == "a" {
			for _, attribute := range n.Attr {
				if attribute.Key == "href" {
					href := attribute.Val

					// this is just to clean the files it reads
					if href == "" || strings.HasPrefix(href, "#") {
						break
					}
					
					// this will probably be gone once moving to the web
					ext := strings.ToLower(path.Ext(href))
					ext = strings.ReplaceAll(ext, "\\", "")
					ext = strings.Trim(ext, "\"")

					// if its a website or a html file it will append

					if ext == ".html" || ext == "" {
						hrefs = append(hrefs, href)
					}
					break
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}

	f(doc)
	// fmt.Println((page_map))
	return words, hrefs
}
// cleans urls
func clean(host, path string) string {
    // Handle nil or empty strings
    if host == "" || path == "" {
        return ""
    }

    base, err := url.Parse(host)
    if err != nil {
        // Return original path if base URL can't be parsed
        return path
    }

    ref, err := url.Parse(path)
    if err != nil {
        // Return original path if reference URL can't be parsed
        return path
    }

    // If reference URL lacks scheme (http/https), copy from base
    if ref.Scheme == "" {
        ref.Scheme = base.Scheme
    }

    // If reference URL lacks hostname, copy from base
    if ref.Host == "" {
        ref.Host = base.Host
    }

    // Remove fragments
    ref.Fragment = ""
    
    // Convert URL struct back to string with all components
    return ref.String()
}