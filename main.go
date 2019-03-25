package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

type JFrogStats struct {
	Downloads uint64 `json:"downloads"`
}

type JFrogItem struct {
	Repo string `json:"repo"`
	Path string `json:"path"`
	Name string `json:"name"`
	Stats []JFrogStats `json:"stats"`
}

type JFrogRange struct {
	Start uint64 `json:"start_pos"`
	End   uint64 `json:"end_pos"`
	Total uint64 `json:"total"`
}

type JFrogResult struct {
	Items []JFrogItem `json:"results"`
	Range JFrogRange `json:"range"`
}

type JFrogTopResults struct {
	Top1 JFrogResult `json:"top_one"`
	Top2 JFrogResult `json:"top_two"`
}

type JFrogConfig struct {
	api_conf string
	api_host string
	api_key	 string
    api_json string
}


func usage() {
	fmt.Printf(
		"Usage: %s [args] ...\n"           +
		"Where arg is:\n"                  +
		"    -conf=<configuration file>\n" +
		"    -host=<hostname>\n"           +
		"    -key=<API key>\n"             +
		"    -json=<Yes|No>\n\n",
		os.Args[0])
}

func parseCommandLine(config *JFrogConfig) {
	if config == nil {
		log.Fatal("parseCommandLine: ERROR: NULL pointer")
	}

	flag.StringVar(&config.api_conf, "conf", "", "Configuration File")
	flag.StringVar(&config.api_host, "host", "", "Hostname")
	flag.StringVar(&config.api_key, "key", "", "API Key")
	flag.StringVar(&config.api_json, "json", "", "Export JSON")

	flag.Usage = usage
	flag.Parse()

	if len(os.Args) == 1 {
		usage()
		os.Exit(1)
	}
}

/*
 *	Read and parse jfrog configuration file. This function will parse
 *	out the API host and key from configuration file into the
 *	corresponding structure.
 */
func parseConfigFile(config *JFrogConfig) {
	if config == nil {
		log.Fatal("parseConfigFile: ERROR: NULL config\n")

	} else if config.api_conf == "" {
		log.Fatal("parseConfigFile: ERROR: No configuration specified\n")
	}

	file, err := os.Open(config.api_conf)
	if err != nil {
		log.Fatal(fmt.Sprintf("parseConfigFile: ERROR: %s\n", err))
	}

	defer file.Close()

	/* XXX: plen needs to match ptr array size */
	plen := 3
	ptr := [3]*string{0:&config.api_host,
		1:&config.api_key, 2:&config.api_json}
	set := false
	idx := -1

	line_scanner := bufio.NewScanner(file)
	line_scanner.Split(bufio.ScanLines)
	for line_scanner.Scan() {
		line := line_scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}

		words := strings.Fields(line)
		for _, word := range words {
			switch strings.ToLower(word) {
				/*
				 * If a value isn't set yet, we can set it. Otherwise,
				 * let the command line argument override the value
				 * specified in the configuration file.
				 */
				case "api_host":
					if config.api_host == "" {
						idx = 0
					}
				case "api_key":
					if config.api_key == "" {
						idx = 1
					}
				case "api_json":
					idx = 2
				case "=":
					set = true
				default:
					if (idx > -1 && idx < plen) && set == true {
						*ptr[idx] = word
						set = false
						idx = -1
					}
			}
		}
	}
}

/*
 *	Given an array of JFRogItem's, try to find the number of
 *	downloads greater than zero. The array passed to this 
 *	function will either have zero downloads, or identical 
 *	downloads for all elements.
 */
func getDownloads(items []JFrogItem) uint64 {
	for _, item := range items {
		downloads := item.Stats[0].Downloads
		if downloads > 0 {
			return downloads
		}
	}

	return 0
}

/*
 *	Find the top 2 downloads. We keep track of items with identical
 *	downloads. So if an item has the same number of downloads as one of
 *	the top 2 items, it is appended to the corresponding list.
 */
func getTopDownloads(in <-chan *JFrogResult, out chan<- []JFrogItem) {
	results := <-in

	top1 := []JFrogItem{JFrogItem{
		Stats: []JFrogStats{JFrogStats{Downloads: 0},}}}
	top2 := []JFrogItem{JFrogItem{
		Stats: []JFrogStats{JFrogStats{Downloads: 0},}}}

	for _, item := range results.Items {
		downloads := item.Stats[0].Downloads

		top1_downloads := getDownloads(top1)
		top2_downloads := getDownloads(top2)

		if downloads > top1_downloads {
			top1 = []JFrogItem{item}

		} else if downloads == top1_downloads {
			top1 = append(top1, item)

		} else if downloads > top2_downloads &&
			downloads != top1_downloads {
			top2 = []JFrogItem{item}

		} else if downloads == top2_downloads &&
			downloads != top1_downloads {
			top2 = append(top2, item)
		}
	}

	out <- top1
	out <- top2
}

/*
 *	Get array of JSON items from JFrog Artifactory server. We construct
 *	a query that will return all jar files with downloads greater than
 *	zero. The result will return the items name and number of downloads.
 *	Ideally, we would like to sort() the results on the server by number
 *	of downloads and limit the top 2 results, however, this does not
 *	work as expected for some reason. Therefore, we have to do the work
 *	ourselves.
 */
func getJFrogItems(out chan<- *JFrogResult, config *JFrogConfig) {
	if config == nil {
		log.Fatal("getJFrogItems: ERROR: NULL config")

	} else if config.api_host == "" || config.api_key == "" {
		log.Fatal("getJFrogItems: ERROR: NULL host or api key")
	}

	api_fmt := "http://%s/artifactory/api/search/aql"
	api_url := fmt.Sprintf(api_fmt, config.api_host)

    payload := `items.find({
			"name": { "$match" : "*.jar" },
			"$and": [
				{ "stat.downloads": { "$gt": "0" } }
			]
		}).include(
			"repo", "name", "path", "stat.downloads"
	)`

	req, err := http.NewRequest(http.MethodPost, api_url,
		bytes.NewReader([]byte(payload)))
	if err != nil {
		log.Fatal(fmt.Sprintf("getJFrogItems: ERROR: %s", err))
	}

	/* Custom JFrog header for authentication using an API key */
	req.Header.Set("X-JFrog-Art-Api", config.api_key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "text/plain")

	client := &http.Client{}

	response, err := client.Do(req)
	if err != nil {
		log.Fatal(fmt.Sprintf("getJFrogItems: ERROR: %s\n", err))
	}

	if response.StatusCode != 200 {
		log.Fatal("getJFrogItems: ERROR: HTTP status is not 200\n")
	}

	defer response.Body.Close()

	var results = new(JFrogResult)
	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&results)
	if err != nil {
		log.Fatal(fmt.Sprintf("getJFrogItems: ERROR: %s\n", err))
	}

	out <- results
}

func showTopDownloadsJSON(top1 []JFrogItem, top2 []JFrogItem) {
	top1_length := uint64(len(top1))
	top2_length := uint64(len(top2))

	top1_range := JFrogRange{Start:0, End:top1_length, Total:top1_length}
	top2_range := JFrogRange{Start:0, End:top2_length, Total:top2_length}

	top1_result := JFrogResult{Items:top1, Range:top1_range}
	top2_result := JFrogResult{Items:top2, Range:top2_range}

	top_results := JFrogTopResults{Top1:top1_result, Top2:top2_result}
	top_json, err := json.MarshalIndent(top_results, "", "\t")
	if err != nil {
		log.Fatal(fmt.Sprintf("showTopDownloadsJSON: ERROR: JSON Marshal"))
	}

	fmt.Printf("%s\n", top_json)
}

func showTopDownloadsNormal(top1 []JFrogItem, top2 []JFrogItem) {
	top1_downloads := getDownloads(top1)
	top2_downloads := getDownloads(top2)

	fmt.Printf("Top Downloads #1 [%d]:\n", top1_downloads)
	fmt.Printf("-------------------------------\n")
	for i, item := range top1 {
		fmt.Printf("%2d. %s\n", i+1, item.Name)
	}

	fmt.Printf("\nTop Downloads #2 [%d]\n", top2_downloads)
	fmt.Printf("-------------------------------\n")
	for i, item := range top2 {
		fmt.Printf("%2d. %s\n", i+1, item.Name)
	}
}

/*
 *	Show the top 2 downloads. If there are multiple jar files with the
 *	same number of downloads, they are all displayed. 
 */
func showTopTwoDownloads(in <-chan []JFrogItem, config *JFrogConfig) {
	top1 := <-in
	top2 := <-in

	show_json := false

	if config.api_json != "" {
		api_json := strings.ToLower(config.api_json)
		if api_json == "true" || api_json == "yes" || api_json == "1" {
			show_json = true
		}
	}

	if show_json == true {
		showTopDownloadsJSON(top1, top2)

	} else {
		showTopDownloadsNormal(top1, top2)
	}
}

func main() {
	var config = JFrogConfig{}

	/* Aquire credentials via command line and/or config file */
	parseCommandLine(&config)
	parseConfigFile(&config)

	results_ch	:= make(chan *JFrogResult)
	items_ch	:= make(chan []JFrogItem)

	defer close(results_ch)
	defer close(items_ch)

	/* Get JSON items from JFrog artifactory server */
	go getJFrogItems(results_ch, &config)

	/* Parse out the top 2 downloads from the returned JSON */
	go getTopDownloads(results_ch, items_ch)

	/* Show the top 2 downloads */
	showTopTwoDownloads(items_ch, &config)
}
