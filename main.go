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
	"strconv"
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
	return `H:\libgendb\asset`
	// dir := filepath.Join(utils.GetBaseDirectory(), "asset")
	// if !utils.Exists(dir) {
	// 	os.MkdirAll(dir, 0655)
	// }
	// return dir
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

var downloadedSignalFile = filepath.Join(utils.GetBaseDirectory(), "downloaded")

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
	defer os.Remove(tempFile)
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
					if utils.GetFileSize(tempFile) != size {
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

var mapLck = sync.Mutex{}

func DeletePartMapKey(parts map[int]Part, key int) {
	mapLck.Lock()
	defer mapLck.Unlock()
	delete(parts, key)
}
func CleanParts(filename string) bool {
	dlrgx := regexp.MustCompile(`(-part-\d+.rar)$`)
	res := true
	for _, part := range utils.GetInfosFromDir(GetAssetDir()) {
		if dlrgx.MatchString(part.FullPath) {
			err := os.Remove(part.FullPath)
			res = res && err == nil
			if !res {
				break
			}
		}
	}
	return res
}
func MergeParts(filename string) error {
	rgx := regexp.MustCompile(`libgen_\d{4,}-\d{2,}-\d{2,}`)
	prefix := rgx.FindString(filename)

	file, err := os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer file.Close()

	dlrgx := regexp.MustCompile(`(-part-\d+.rar)$`)
	digitRgx := regexp.MustCompile(`\D+`)

	parts := map[int]string{}
	size := int64(0)
	mergedBytes := int64(0)

	for _, part := range utils.GetInfosFromDir(GetAssetDir()) {
		if strings.HasPrefix(filepath.Base(part.FullPath), prefix) && dlrgx.MatchString(part.FullPath) {

			idxStr := dlrgx.FindString(part.FullPath)
			idxStr = digitRgx.ReplaceAllString(idxStr, "")
			if num, err := strconv.ParseInt(idxStr, 10, 32); err == nil {
				k := int(num)
				parts[k] = part.FullPath
				size += part.Info.Size()
			}

		}
	}

	keys := make([]int, 0, len(parts))

	for k := range parts {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	total := len(parts)
	counter := 0
	if total > 0 {
		counter++
		fmt.Println("Merging files")
		for _, key := range keys {
			input, err := os.OpenFile(parts[key], os.O_RDONLY, 0755)
			if err != nil {
				return err
			}
			defer input.Close()
			ln, err := io.Copy(file, input)

			if err != nil {
				return err
			}
			input.Close()
			mergedBytes += ln
			progress := (float64(counter) * 100) / float64(total)
			fmt.Printf("Merged (%s/%s) :Progress %.2f%%\n", utils.FormatBytes(mergedBytes), utils.FormatBytes(size), progress)

		}
	}
	if mergedBytes != size {
		return errors.New("file sizes do not match")
	}
	return nil
}
func VerifyCompletion(filename string, total int64) bool {
	rgx := regexp.MustCompile(`libgen_\d{4,}-\d{2,}-\d{2,}`)
	prefix := rgx.FindString(filename)
	downloaded := int64(0)
	for _, part := range utils.GetInfosFromDir(GetAssetDir()) {
		if strings.HasPrefix(filepath.Base(part.FullPath), prefix) && strings.HasSuffix(strings.ToLower(part.FullPath), ".rar") {
			downloaded += part.Info.Size()
		}
	}

	return downloaded == total
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

			downloaded := int64(0)

			var asset = GetAssetDir()
			dlrgx := regexp.MustCompile(`(-part-\d+.rar)$`)
			digitRgx := regexp.MustCompile(`\D+`)

			for _, inf := range utils.GetInfosFromDir(asset) {
				if !inf.Info.IsDir() && dlrgx.MatchString(inf.FullPath) {
					downloaded += inf.Info.Size()
					idxStr := dlrgx.FindString(inf.FullPath)
					idxStr = digitRgx.ReplaceAllString(idxStr, "")
					if num, err := strconv.ParseInt(idxStr, 10, 32); err == nil {
						k := int(num) - 1
						delete(parts, k)
					}

				}
			}
			// {
			// 	err := DownloadPart(destFile, link, 265, 2048*10, 1024*1024*5)
			// 	fmt.Println(err)
			// }
			wg := sync.WaitGroup{}
			for len(parts) > 0 {

				keys := make([]int, 0, len(parts))

				for k := range parts {
					keys = append(keys, k)
				}
				sort.Ints(keys)

				total := len(parts)
				fmt.Printf("Downloading %d parts\n", total)

				downloading := 0
				start := time.Now()
				for _, idx := range keys {
					// if slices.Contains(downloadedIndexes, idx+1) {
					// 	continue
					// }
					wg.Add(1)
					p := parts[idx]
					downloading++
					go func(index int, part Part) {
						defer func() {
							downloading--

							wg.Done()

						}()
						err := DownloadPart(destFile, link, index, part.Start, part.Size)
						if err == nil {
							DeletePartMapKey(parts, index)
							downloaded += part.Size
						}

					}(idx, p)
					if time.Since(start) > (time.Second*5) && downloaded > 0 {
						progress := float64((downloaded * 100) / size)
						fmt.Printf("Downloaded %s/%s :progress %.2f%%\n", utils.FormatBytes(downloaded), utils.FormatBytes(size), progress)
						start = time.Now()
					}
					for downloading > 4 {
						time.Sleep(time.Second * 2)
					}

				}
				wg.Wait()
				time.Sleep(time.Second * 2)
			}
			wg.Wait()
			if VerifyCompletion(destFile, size) {
				err := MergeParts(destFile)
				if err == nil {

				}
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
				Size:  int64(rem),
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
		fmt.Println("Another instance is running")
		time.Sleep(time.Second * 10)
	}
}
