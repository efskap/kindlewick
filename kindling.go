package main

import (
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly"
	_ "github.com/gocolly/colly"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"
)

// I don't think Kindles have any way of looking up defintions with spaces in them
const skipMultiWordDefinitions = true

func main() {
	skipSections := map[string]bool{
		"Derived terms":    true,
		"Compounds":        true,
		"References":       true,
		"Further reading":  true,
		"Synonyms":         true,
		"Antonyms":         true,
		"Anagrams":         true,
		"Declension":       true,
		"Coordinate terms": true,
		"See also":         true,
		"Related terms":    true,
		"Hypernyms":        true,
		"Hyponyms":         true,
		"Conjugation":      true,
	}

	skipPrefixes := map[string]bool{
		"Hyphenation: ": true,
		"Rhymes: ":      true,
		"(index ":       true,
	}
	var words []Word
	srcLang := "Finnish"
	// Instantiate default collector
	c := colly.NewCollector(
		// Visit only domains: coursera.org, www.coursera.org
		colly.AllowedDomains("en.wiktionary.org"),

		// Cache responses to prevent multiple download of pages
		// even if the collector is restarted
		colly.CacheDir("./.kindling_cache"),
		//colly.Async(true),
		colly.Async(false),
	)

	c.SetProxy("socks5://localhost:1337")
	// On every a element which has .top-matter attribute call callback
	// This class is unique to the div that holds all information about a story
	srcLangHeading := "#" + srcLang
	c.OnHTML("div.mw-parser-output", func(e *colly.HTMLElement) {
		word := Word{}
		sel := e.DOM.Find(srcLangHeading). // find span with our lang
							Parent(). // get the h2 that it's in
							NextUntil("hr, h2")

		headword := sel.Find(".headword").First().Text()
		//fmt.Println("Word:", headword)
		word.Headword = headword
		sel.Find("h3").Each(func(i int, selection *goquery.Selection) {
			selection.ReplaceWithHtml(fmt.Sprintf("<b>%s</b>", selection.Text()))
		})
		infl := make(map[string]struct{})
		sel.Find("td > span").Each(func(i int, selection *goquery.Selection) {
			//fmt.Println("infl:", selection.Text())
			spl := strings.Split(selection.Text(), " ")
			last := spl[len(spl)-1]
			infl[last] = struct{}{}
		})
		delete(infl, headword)
		for inf := range infl {
			word.Inflections = append(word.Inflections, inf)
		}
		sel.Find("a, span").Each(func(i int, selection *goquery.Selection) {
			selection.ReplaceWithHtml(selection.Text())
		})
		sel.Find("style").Remove()
		sel.Find("li").Each(func(i int, selection *goquery.Selection) {
			if selection.Nodes[0].Data == "table" || selection.Nodes[0].Data == "style" || selection.Nodes[0].Data == "div" {
				selection.Remove()
			}
			text := selection.Text()
			for prefix := range skipPrefixes {
				if strings.HasPrefix(text, prefix) {
					selection.Remove()
					return
				}
			}
		})
		//sel.Find("li").Each(func(i int, selection *goquery.Selection) {
		//	selection.ReplaceWithHtml(fmt.Sprintf("<p>%s</p>", selection.Text()))
		//})
		var fullHtml strings.Builder

		skipNext := false
		sel.Each(func(i int, selection *goquery.Selection) {
			if skipNext {
				skipNext = false
				return
			}
			if selection.Nodes[0].Data == "table" || selection.Nodes[0].Data == "style" || selection.Nodes[0].Data == "div" {
				return
			}
			text := selection.Text()
			for prefix := range skipPrefixes {
				if strings.HasPrefix(text, prefix) {
					return
				}
			}
			if strings.HasPrefix(selection.Nodes[0].Data, "h") {
				selection.Nodes[0].Data = "h4"
				if skipSections[text] {
					skipNext = true
					return
				}
			}
			html, err := goquery.OuterHtml(selection)
			if err != nil {
				log.Println("Failed to get html for word", headword)
				return
			}
			if html != "" {
				fullHtml.WriteString(html)

			}

		})
		word.Html = fullHtml.String()
		words = append(words, word)
	})

	// Set max Parallelism and introduce a Random Delay
	_ = c.Limit(&colly.LimitRule{
		Parallelism: 1,
		//Delay:500 * time.Millisecond,
		//RandomDelay: 1000 * time.Millisecond,
	})

	c.OnError(func(response *colly.Response, e error) {
		//log.Println("Error getting", response.Request.URL.String())
		//log.Println(e)
		c.Visit(response.Request.URL.String())
	})
	// Before making a request print "Visiting ..."
	c.OnResponse(func(response *colly.Response) {
		log.Println("Parsing", response.Request.URL)
	})
	c.Visit("https://en.wiktionary.org/wiki/mutta?printable=yes")
	titles := make(chan string, 2)
	go findPagesInCategory(fmt.Sprintf("%s_lemmas", srcLang), titles)
	i := 1
	for title := range titles {
		c.Visit(fmt.Sprintf("https://en.wiktionary.org/wiki/%s?printable=yes", title))
		//if i >= 1500 {
		//	break
		//}
		i++
	}

	c.Wait()
	println("Titles found:", i)
	println("Docs parsed:", len(words))
	fmt.Println("Writing template...")
	tmpl := template.Must(template.ParseFiles("dict.gohtml"))
	f, err := os.Create("dict.html")
	err = tmpl.Execute(f, DictData{Words: words, SrcLang: "Finnish", SrcLangCode: "fi"})
	if err != nil {
		log.Print("execute: ", err)
		return
	}
}

type CmResponse struct {
	Continue struct {
		Cmcontinue string `json:"cmcontinue"`
	} `json:"continue"`
	Query struct {
		Categorymembers []struct {
			Title string `json:"title"`
		} `json:"categorymembers"`
	} `json:"query"`
}

func findPagesInCategory(category string, titles chan<- string) {
	baseUrl := fmt.Sprintf("https://en.wiktionary.org/w/api.php?action=query&cmlimit=500&format=json&list=categorymembers&formatversion=2&cmtitle=Category:%s&cmprop=title", category)
	cont := " "
	for cont != "" {
		currentUrl := baseUrl + "&cmcontinue=" + cont
		log.Println("Getting", currentUrl)
		response, err := http.Get(currentUrl)
		if err != nil {
			log.Println("Error getting", currentUrl)
			log.Println(err)
			time.Sleep(2 * time.Second)
			log.Println("Trying again.")
			continue
		}
		data := CmResponse{}
		err = json.NewDecoder(response.Body).Decode(&data)
		if err != nil {
			log.Println("Error decoding", currentUrl)
			log.Println(err)
			log.Println(err)
			time.Sleep(2 * time.Second)
			log.Println("Trying again.")
			continue
		}
		for _, cm := range data.Query.Categorymembers {
			if skipMultiWordDefinitions && strings.Contains(cm.Title, "_") {
				continue
			}
			titles <- cm.Title
		}
		cont = data.Continue.Cmcontinue
		time.Sleep(500 * time.Millisecond)
	}
	close(titles)
}

type Word struct {
	Headword    string
	Inflections []string
	Html        string
}
type DictData struct {
	Words       []Word
	SrcLangCode string
	SrcLang     string
}
