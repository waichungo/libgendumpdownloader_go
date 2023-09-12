package main

import (
	"bytes"
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

var LibGenFullRgx = `libgen_\d{4,}-\d{2,}-\d{2,}`

type Part struct {
	Start int64
	Size  int64
}

func GetAssetDir() string {
	// return `H:\libgendb\asset`
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
		rgx = regexp.MustCompile(LibGenFullRgx)
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
		rgx := regexp.MustCompile(LibGenFullRgx)
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
func GetPart(link string, start, size int64) []byte {

	for trys := 0; trys < 5; trys++ {
		res, err := utils.GetResponse(link, &map[string]string{
			"Range": fmt.Sprintf("bytes=%d-%d", start, (start+size)-1),
		})
		if err == nil {
			if res.ContentLength != size {
				return nil
			}
			defer res.Body.Close()
			bytesBuffer := make([]byte, 0, 1024)
			writer := bytes.NewBuffer(bytesBuffer)

			if err != nil {
				return nil
			}

			ln, err := io.CopyN(writer, res.Body, size)

			if err == nil || ln == size {
				return writer.Bytes()

			}

		}
		time.Sleep(time.Second)
		utils.WaitForConnection()
	}
	return nil
}

var mapLck = sync.Mutex{}

func DeletePartMapKey(parts map[int]Part, key int) {
	mapLck.Lock()
	defer mapLck.Unlock()
	delete(parts, key)
}
func CleanDownloadedParts() bool {
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
func VerifyPartsFromNetwork(link, filename string, totalSize, partSize int64) bool {

	splitParts := SplitFileParts(totalSize, int(partSize))
	networkBufferMap := map[int][]byte{}
	netWg := sync.WaitGroup{}

	netDownloading := 0
	for key, _part := range splitParts {
		netDownloading++
		netWg.Add(1)
		go func(part Part, idx int) {
			defer func() {
				netDownloading--
				netWg.Done()

			}()
			partBufferSize := 1024
			if partBufferSize > int(partSize) {
				partBufferSize = int(partSize)
			}
			buff := GetPart(link, part.Start, int64(partBufferSize))
			networkBufferMap[idx] = buff

		}(_part, key)
		for netDownloading > 5 {
			time.Sleep(time.Second)
		}

	}
	netWg.Wait()

	dlrgx := regexp.MustCompile(`(-part-\d+.rar)$`)
	digitRgx := regexp.MustCompile(`\D+`)
	// equal := true

	parts, err := GetSortedParts(filename)

	equal := true
	if err == nil {
		for _, part := range parts {

			idxStr := dlrgx.FindString(part)
			idxStr = digitRgx.ReplaceAllString(idxStr, "")
			if num, err := strconv.ParseInt(idxStr, 10, 32); err == nil {
				key := int(num) - 1

				netPartBuffer := networkBufferMap[key]

				partFile, err := os.OpenFile(part, os.O_RDONLY, 0755)
				if err != nil {
					continue
				}
				defer partFile.Close()

				partBuffer := make([]byte, len(netPartBuffer))

				n, err := io.ReadFull(partFile, partBuffer)
				if err != nil || n != len(partBuffer) {
					return false
				}

				if !bytes.EqualFold(partBuffer, netPartBuffer) {
					equal = false
					partFile.Close()
					os.Remove(part)
				} else {
					equal = equal && true
				}

			}
		}
	} else {
		return false
	}

	return err == nil && equal
}
func VerifyBytes(filename string) bool {
	dlrgx := regexp.MustCompile(`(-part-\d+.rar)$`)
	digitRgx := regexp.MustCompile(`\D+`)
	// equal := true

	destFile, err := os.OpenFile(filename, os.O_RDWR, 0755)
	destSize := utils.GetFileSize(filename)
	if err == nil {
		defer destFile.Close()
		parts, err := GetSortedParts(filename)
		if err == nil {
			for _, part := range parts {

				idxStr := dlrgx.FindString(part)
				idxStr = digitRgx.ReplaceAllString(idxStr, "")
				if num, err := strconv.ParseInt(idxStr, 10, 32); err == nil {
					key := int(num) - 1

					partSize := utils.GetFileSize(part)
					partFile, err := os.OpenFile(part, os.O_RDONLY, 0755)
					if err != nil {
						return false
					}
					defer partFile.Close()
					destPos := partSize * int64(key)
					if destPos > destSize {
						return false
					}
					bufferSize := 1024
					if (destSize - destPos) < int64(bufferSize) {
						bufferSize = int(destSize - destPos)
					}

					destBuffer := make([]byte, bufferSize)
					partBuffer := make([]byte, bufferSize)
					destFile.Seek(destPos, 0)

					n, err := io.ReadFull(destFile, destBuffer)
					if err != nil || n != len(destBuffer) {
						return false
					}
					n, err = io.ReadFull(partFile, partBuffer)
					if err != nil || n != len(destBuffer) {
						return false
					}
					equal := false
					if !bytes.EqualFold(partBuffer, destBuffer) {

						destFile.Seek(destPos, 0)
						partFile.Seek(0, 0)

						ln, _ := io.Copy(destFile, partFile)
						if ln == partSize {
							partFile.Seek(0, 0)
							destFile.Seek(destPos, 0)

							n, err = io.ReadFull(destFile, destBuffer)
							if err != nil || n != len(destBuffer) {
								return false
							}
							n, err = io.ReadFull(partFile, partBuffer)
							if err != nil || n != len(destBuffer) {
								return false
							}

							if bytes.EqualFold(partBuffer, destBuffer) {
								equal = true
							}

						}

					} else {
						equal = true
					}
					partFile.Close()
					if !equal {
						return false
					}
				}
			}
		} else {
			return false
		}
	}

	return err == nil
}
func MergeParts(filename string) error {
	parts, err := GetSortedParts(filename)
	if err != nil {
		return err
	}
	size := int64(0)
	for _, part := range parts {
		size += utils.GetFileSize(part)
	}

	if utils.Exists(filename) {
		if utils.GetFileSize(filename) == size {
			if VerifyBytes(filename) {
				return nil
			}
		}
		os.Remove(filename)
	}
	file, err := os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer file.Close()

	mergedBytes := int64(0)

	total := len(parts)
	counter := 0
	if total > 0 {
		fmt.Println("Merging files")
		for _, filePart := range parts {
			counter++
			input, err := os.OpenFile(filePart, os.O_RDONLY, 0755)
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
	if !VerifyBytes(filename) {
		return errors.New("bytes do not match")
	}
	return nil
}
func GetSortedParts(filename string) ([]string, error) {
	result := make([]string, 0, 10)
	rgx := regexp.MustCompile(LibGenFullRgx)
	prefix := rgx.FindString(filename)

	dlrgx := regexp.MustCompile(`(-part-\d+.rar)$`)
	digitRgx := regexp.MustCompile(`\D+`)

	parts := map[int]string{}
	size := int64(0)

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
	for idx, k := range keys {
		next := k + 1
		prev := k - 1
		if k > 1 {
			if keys[idx-1] != prev {
				return result, errors.New("file part missing")
			}
		}
		if k < len(keys) {
			if keys[idx+1] != next {
				return result, errors.New("file part missing")
			}
		}
	}
	for _, k := range keys {
		result = append(result, parts[k])
	}

	return result, nil
}
func VerifyCompletion(filename string, total int64) bool {
	rgx := regexp.MustCompile(LibGenFullRgx)
	dlrgx := regexp.MustCompile(`(-part-\d+.rar)$`)
	prefix := rgx.FindString(filename)
	downloaded := int64(0)
	for _, part := range utils.GetInfosFromDir(GetAssetDir()) {
		if strings.HasPrefix(filepath.Base(part.FullPath), prefix) && dlrgx.MatchString(part.FullPath) {
			downloaded += part.Info.Size()
		}
	}

	return downloaded == total
}
func Start() bool {

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
		if !VerifyPartsFromNetwork(link, destFile, size, partSize) {
			return false
		}
		if VerifyBytes(destFile) {
			return CleanDownloadedParts()
		} else if VerifyCompletion(destFile, size) {
			err := MergeParts(destFile)
			if err == nil {
				return CleanDownloadedParts()
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
	if utils.FirstInstance() && !utils.Exists(downloadedSignalFile) {
		completed := Start()
		for !completed {
			time.Sleep(time.Second * 10)
			completed = Start()
		}
		if completed {
			utils.WriteFile(downloadedSignalFile, []byte(""))
		}

	} else {
		fmt.Println("Another instance is running")
		time.Sleep(time.Second * 10)
	}
}
