package main

import (
	"errors"
	"fmt"
	"io"
	"libgen/downloader"
	"libgen/utils"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type Part struct {
	Start int64
	Size  int64
}

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
	rgx := regexp.MustCompile(`((-part-\d+.tmp)$)|((-part-\d+.rar)$)`)
	dir := GetAssetDir()
	infos, err := os.ReadDir(dir)
	if err == nil {
		paths := make([]string, 0, 20)
		for _, info := range infos {
			if !info.IsDir() && (strings.HasSuffix(info.Name(), ".tmp") || strings.HasSuffix(info.Name(), ".rar")) {
				name := info.Name()
				if rgx.MatchString(name) {
					name = rgx.ReplaceAllString(name, "")
				}
				paths = append(paths, name)
			}

		}
		sort.Slice(paths, func(i, j int) bool {
			return paths[i] > paths[j]
		})
		rgx = regexp.MustCompile(`libgen_\d{4,}-\d{2,}-\d{2,}`)
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

func GetDumpToDownload() (string, int64) {
	lastDownload := GetLastDowloadedDump()

	link := ""
	dumps := GetLibgenDumps()
	size := int64(0)

	if len(lastDownload) > 0 {
		for _, dump := range dumps {
			if strings.Contains(dump, lastDownload) {
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
		headers, err := downloader.GetHeaders(link)
		if err == nil {
			size = headers.Size
		}
	}
	return link, size
}

func DownloadPart(destFile, link string, index int, start, size int64) error {
	tempFile := filepath.Join(filepath.Dir(destFile), utils.RemoveExt(filepath.Base(destFile))+fmt.Sprintf("-part-%d.tmp", index+1))
	targetFile := utils.RemoveExt(tempFile) + filepath.Ext(destFile)

	if utils.Exists(targetFile) {

		if utils.GetFileSize(targetFile) == size {
			return nil
		}
		os.Remove(targetFile)
	}
	var err error = nil
	for trys := 0; trys < 5; trys++ {
		res, err := utils.GetResponse(link, &map[string]string{
			"Range": fmt.Sprintf("bytes=%d-%d", start, (start+size)-1),
		})
		if err == nil {
			if res.ContentLength != size {
				return errors.New("partial content does not match size")
			}
			defer res.Body.Close()

			file, err := os.OpenFile(tempFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0755)
			if err != nil {
				return err
			}
			defer file.Close()
			rem := size
			ln := int64(0)
			for rem > 0 {
				ln, err = io.CopyN(file, res.Body, 1024*20)

				if err == io.EOF {
					err = nil
					file.Close()
					if utils.GetFileSize(targetFile) != size {
						err = errors.New("file size does not match")
					}
					break
				}
				if err != nil {
					file.Close()
					break
				}
				rem -= ln
			}
			file.Close()
			if err == nil {
				return utils.MoveOrCopyFile(tempFile, targetFile)

			}
		}
		time.Sleep(time.Second)
		utils.WaitForConnection()
	}
	return err
}
func Start() bool {

	if !utils.Exists(downloadedSignalFile) {
		link, size := GetDumpToDownload()
		if size > 0 {
			const partSize = 1024 * 1024 * 20
			filename := ""
			slashIdx := strings.LastIndex(link, "/")
			filename = link[slashIdx+1:]
			destFile := filepath.Join(GetAssetDir(), filename)

			parts := SplitFileParts(size, partSize)

			// {
			// 	err := DownloadPart(destFile, link, 265, 2048*10, 1024*1024*5)
			// 	fmt.Println(err)
			// }

			for len(parts) > 0 {
				wg := sync.WaitGroup{}
				downloading := 0

				for idx, p := range parts {
					wg.Add(1)
					downloading++
					go func(index int, part Part) {
						defer func() {
							downloading--
							wg.Done()

						}()
						err := DownloadPart(destFile, link, index, part.Start, part.Size)
						if err == nil {
							delete(parts, index)
						}

					}(idx, p)
					for downloading > 4 {
						time.Sleep(time.Second * 2)
					}
				}
				wg.Wait()

			}

		}

	}
	return false
}
func SplitFileParts(totalSize int64, partSize int) map[int]Part {
	var res = map[int]Part{}
	rem := totalSize
	index := 0
	startIdx := int64(0)
	for rem > 0 {
		if rem > int64(partSize) {
			res[index] = Part{
				Start: int64(startIdx),
				Size:  int64(partSize),
			}
			rem -= int64(partSize)
			startIdx += int64(partSize)
		} else {
			res[index] = Part{
				Start: int64(startIdx),
				Size:  int64(partSize),
			}
			startIdx += rem
			rem -= rem
		}
		index++
	}

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
