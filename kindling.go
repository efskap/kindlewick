package main

import (
	"bufio"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/PuerkitoBio/goquery"
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

	filenamePtr := flag.String("file", "", "gob file to load words from instead of wiktionary")
	freqlistPtr := flag.String("freqlist", "fi.txt", "frequency list to filter by")
	flag.Parse()
	var words []Word
	if *filenamePtr == "" {
		words = downloadWords("Finnish")
		fmt.Println("Saving words to words.gob")
		err := writeGob("words.gob", words)
		if err != nil {
			log.Println("Error saving file", err)
		}
	} else {
		err := readGob(*filenamePtr, &words)
		if err != nil {
			log.Fatalln("Error loading file", err)
		}
	}

	if *freqlistPtr != "" {
		file, err := os.Open(*freqlistPtr)
		if err != nil {
			log.Println("Error loading file", err)
		} else {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			freqWords := make(map[string]bool)
			for scanner.Scan() {
				split := strings.TrimSpace(strings.Split(scanner.Text(), " ")[0])
				freqWords[split] = true
			}
			fmt.Println(len(freqWords), "words in frequency list")

			var newWords []Word
			for _, word := range words {
				if freqWords[word.Headword] {
					newWords = append(newWords, word)
				} else {
					for _, infl := range word.Inflections {
						if freqWords[infl] {
							newWords = append(newWords, word)
							break
						}
					}
				}
			}

			fmt.Println("Freq filtering:", len(words), "->", len(newWords))
			words = newWords
		}
	}

	fmt.Println("Writing template...")
	tmpl := template.Must(template.ParseFiles("dict.gohtml"))

	f, err := os.Create("dict.html")
	err = tmpl.Execute(f, DictData{Words: words, SrcLang: "Finnish", SrcLangCode: "fi"})
	if err != nil {
		log.Print("execute: ", err)
	}

	batchNum := 1
	batchSize := 10000
	if false && len(words) > batchSize {
		fmt.Println("Now trying in batches...")
		for i := 0; i < len(words); i += batchSize {
			batch := words[i:min(i+batchSize, len(words))]
			f, err := os.Create(fmt.Sprintf("dict_part_%d.html", batchNum))
			err = tmpl.Execute(f, DictData{Words: batch, SrcLang: "Finnish", SrcLangCode: "fi"})
			if err != nil {
				log.Print("error executing tmpl: ", err)
			}
			batchNum++
		}
	}
}
func downloadWords(srcLang string) (words []Word) {

	parser := WktParser{}
	parser.SkipSections = map[string]bool{
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

	parser.SkipPrefixes = map[string]bool{
		"Hyphenation: ": true,
		"Rhymes: ":      true,
		"(index ":       true,
	}
	parser.SrcLang = srcLang
	parser.SrcLangHeading = "#" + parser.SrcLang
	titles := make(chan string, 1000)
	titles <- "mutta"
	titles <- "olla"
	go findPagesInCategory(fmt.Sprintf("%s_lemmas", parser.SrcLang), titles)
	i := 1
	for title := range titles {
		currentUrl := fmt.Sprintf("https://en.wiktionary.org/w/api.php?action=parse&formatversion=2&format=json&page=%s&prop=text", title)
		for {
			response, err := http.Get(currentUrl)
			fmt.Println(i, "Getting", currentUrl)
			if err != nil {
				log.Println("Retrying", currentUrl)
				log.Println(err)
				time.Sleep(2 * time.Second)
				continue
			}

			data := ParseResponse{}
			err = json.NewDecoder(response.Body).Decode(&data)
			if err != nil {
				log.Println("Error decoding", currentUrl)
				log.Println(err)
				time.Sleep(2 * time.Second)
				continue
			}

			word, err := parser.parseHtml(data.Parse.Text)
			if err != nil {
				log.Println("Error parsing", currentUrl)
				log.Println(err)
				time.Sleep(2 * time.Second)
				continue
			}
			words = append(words, word)
			break
		}
		time.Sleep(50 * time.Millisecond)
		//if i >= 10 {
		//	break
		//}

		i++
	}

	println("Titles found:", i)
	println("Docs parsed:", len(words))
	return words
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func writeGob(filePath string, object interface{}) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := gob.NewEncoder(file)
	err = encoder.Encode(object)
	return err
}
func readGob(filePath string, object interface{}) error {
	file, err := os.Open(filePath)
	if err == nil {
		decoder := gob.NewDecoder(file)
		err = decoder.Decode(object)
	}
	file.Close()
	return err
}

type ParseResponse struct {
	Parse struct {
		Title  string `json:"title"`
		Pageid int    `json:"pageid"`
		Text   string `json:"text"`
	} `json:"parse"`
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

type WktParser struct {
	SrcLang        string
	SrcLangHeading string
	SkipSections   map[string]bool

	SkipPrefixes map[string]bool
}

func (p *WktParser) parseHtml(html string) (Word, error) {
	word := Word{}
	e, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return word, err
	}
	sel := e.Find(p.SrcLangHeading). // find span with our lang
						Parent(). // get the h2 that it's in
						NextUntil("hr, h2")

	sel.Find("style, .mw-editsection").Remove()
	sel.Find(".audiotable").Parent().Remove()

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
	sel.Find("li").Each(func(i int, selection *goquery.Selection) {
		if selection.Nodes[0].Data == "table" || selection.Nodes[0].Data == "style" || selection.Nodes[0].Data == "div" {
			selection.Remove()
		}
		text := selection.Text()
		for prefix := range p.SkipPrefixes {
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
		for prefix := range p.SkipPrefixes {
			if strings.HasPrefix(text, prefix) {
				return
			}
		}
		if strings.HasPrefix(selection.Nodes[0].Data, "h") {
			selection.Nodes[0].Data = "h3"
			if p.SkipSections[text] {
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
	return word, nil
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
			if skipMultiWordDefinitions && strings.Contains(cm.Title, " ") {
				continue
			}
			// EXPERIMENT: dont use articles with hyphen in them
			if strings.Contains(cm.Title, "-") {
				continue
			}
			if strings.HasPrefix(cm.Title, "Category:") {
				continue
			}
			titles <- cm.Title
		}
		cont = data.Continue.Cmcontinue
		time.Sleep(2 * time.Second)
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
