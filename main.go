package main

import (
	"libgen/utils"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func GetAssetDir() string {
	dir := filepath.Join(utils.GetBaseDirectory(), "asset")
	if !utils.Exists(dir) {
		os.MkdirAll(dir, 0655)
	}
	return dir
}
func GetLibgenDumps() []string {
	dumps := make([]string, 0, 20)
	var dumpUrl = "https://data.library.bz/dbdumps/"
	resp, err := http.Get(dumpUrl)
	trys := 0
	for err != nil {
		resp, err = http.Get(dumpUrl)
		if err == nil {
			break
		}
		trys++
		time.Sleep(time.Second)
		utils.WaitForConnection()
		if err != nil && trys > 5 {
			return dumps
		}
	}
	defer resp.Body.Close()
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err == nil {

		doc.Find("tr").Each(func(i int, s *goquery.Selection) {
			anchor := s.Find("td > a").First()
			if anchor != nil {
				link := anchor.AttrOr("href", "")
				if len(link) > 0 && strings.HasSuffix(link, "rar") {
					dumps = append(dumps, link)
				}
			}
		})
	}

	return dumps

}
func GetLastDowloadedDump() string {
	downloaded := ""

	dir := GetAssetDir()
	infos, err := os.ReadDir(dir)
	if err == nil {
		paths := make([]string, 0, 20)
		for _, info := range infos {
			if !info.IsDir() && (strings.HasSuffix(info.Name(), ".tmp") || strings.HasSuffix(info.Name(), ".rar")) {
				paths = append(paths, info.Name())
			}

		}
		sort.Slice(paths, func(i, j int) bool {
			return paths[i] > paths[j]
		})
		rgx := regexp.MustCompile(`libgen_\d{4,}-\d{2,}-\d{2,}`)
		for _, dl := range paths {
			if rgx.MatchString(dl) {
				downloaded = dl
				break
			}
		}
	}
	return downloaded
}

var downloadedSignalFile = filepath.Join(GetAssetDir(), "downloaded")

func Start() bool {

	if !utils.Exists(downloadedSignalFile) {
		lastDownload := GetLastDowloadedDump()

		link := ""
		dumps := GetLibgenDumps()

		if len(lastDownload) > 0 {
			for _, dump := range dumps {
				if strings.Contains(lastDownload, dump) {
					link = dump
					if !strings.HasSuffix(link, ".rar") {
						link = utils.RemoveExt(link)
					}
					break
				}
			}
		}
		if len(link) == 0 {
			utils.DeleteAllFiles(GetAssetDir())
			sort.Slice(dumps, func(i, j int) bool {
				return dumps[i] > dumps[j]
			})
			rgx := regexp.MustCompile(`libgen_\d{4,}-\d{2,}-\d{2,}`)
			for _, dump := range dumps {
				if rgx.MatchString(dump) {
					link = dump
					break
				}
			}
		}
		if len(link) > 0 {

			link = "https://data.library.bz/dbdumps/" + link
			filename := ""
			slashIdx := strings.LastIndex(link, "/")
			filename = link[slashIdx+1:]
			destFile := filepath.Join(GetAssetDir(), filename)

		}

	}
	return false
}
func SplitFileParts(totalSize int64) map[int]int {
	var res = map[int]int{}

	return res
}
func main() {
	if utils.FirstInstance() {
		for !Start() {
			time.Sleep(time.Second * 10)
		}
	} else {
		time.Sleep(time.Second * 10)
	}
}
