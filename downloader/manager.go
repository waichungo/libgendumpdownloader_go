package downloader

import (
	_ "embed"
	"fmt"
	"io"
	"libgen/mimes"
	"libgen/utils"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Headers struct {
	Size int64
	Name string
}

var lck sync.Mutex

type Timespan time.Duration

func (t Timespan) Format(format string) string {
	z := time.Unix(0, 0).UTC()
	return z.Add(time.Duration(t)).Format(format)
}

type DownloadStatus struct {
	ETA        time.Duration
	Downloaded int64
	Progress   int
	Rate       string
}
type DownloadItem struct {
	Name   string
	Link   string
	Status DownloadStatus

	stopped           bool
	DownloadDirectory string
	Size              int64
	Downloading       bool
	dlLck             *sync.Mutex
	dst               string
}
type ProgressState struct {
	Id         int    `json:"id"`
	Name       string `json:"name"`
	Percentage int    `json:"percentage"`

	Remaining uint64 `json:"remaining"`
	Total     uint64 `json:"total"`
	ETA       string `json:"eta"`
	Rate      string `json:"rate"`
	Machine   string `json:"machine"`
	User      string `json:"user"`
}

func GetName(resource string, ext *string) string {
	lnk, err := url.Parse(resource)
	name := resource
	if err == nil {

		path, _ := url.QueryUnescape(lnk.Path)

		path = strings.Trim(path, "/")
		idx := strings.LastIndex(path, "/")
		if idx > 0 && (idx+1) < len(path) {
			name = path[idx+1:]
		}
		if len(name) == 0 {
			splits := strings.Split(lnk.Host, ".")
			if len(splits) > 2 {
				name = splits[len(splits)-2]
			} else if len(splits) == 2 {
				name = splits[0]
			}
		}

	}
	if ext != nil {
		if strings.TrimPrefix(filepath.Ext(name), ".") != strings.TrimPrefix(*ext, ".") {

			name = name + "." + strings.TrimPrefix(*ext, ".")
		}
	}
	return utils.ReplaceInvalidFileChars(name)
}

func GetHeaders(uri string) (*Headers, error) {
	hd := Headers{}
	utils.WaitForConnection()
	res, err := utils.GetResponse(uri, nil)
	var retrys = 0
	for err != nil {

		res, err = utils.GetResponse(uri, nil)
		if err == nil || retrys > 5 {
			break
		}
		utils.WaitForConnection()
		retrys++

	}
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	hd.Size = res.ContentLength

	name := res.Header.Get("Content-Disposition")

	if len(name) == 0 {

		ctype := res.Header.Get("Content-Type")

		if ctype != "" {
			ext := mimes.GetExtensionForMime(ctype)
			name = GetName(uri, &ext)
		} else {
			name = GetName(uri, nil)
		}

	} else {
		_, params, err := mime.ParseMediaType(name)
		if err == nil {
			name = params["filename"] // set to "foo.png"
		}
	}
	hd.Name = utils.ReplaceInvalidFileChars(name)
	return &hd, nil
}

func CanResume(uri string) bool {
	sz := []int64{0, 0}
	wg := sync.WaitGroup{}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var hd map[string]string
			if idx == 1 {
				hd = map[string]string{
					"Range": "bytes=1024-",
				}
			}
			res, err := utils.GetResponse(uri, &hd)
			if err == nil {
				defer res.Body.Close()
				sz[idx] = res.ContentLength
			}

		}(i)
	}
	wg.Wait()
	return sz[0] > 0 && sz[1] > 0 && sz[0] != sz[1]

}

func Download(link, destFile string) (DownloadItem, error) {

	defer func() {
		recover()
	}()

	complete := make(chan error)
	finished := false
	dl := DownloadItem{
		Link:  link,
		dlLck: &sync.Mutex{},
	}
	defer close(complete)
	wg := sync.WaitGroup{}
	updateFunc := func(item *DownloadItem) {
		prLine := fmt.Sprintf("%d%% Downloaded (%s) (%s) %s/%s ETA %f s", item.Status.Progress, item.Name, item.Status.Rate, utils.FormatBytes(item.Status.Downloaded), utils.FormatBytes(item.Size), item.Status.ETA.Seconds())
		fmt.Println(prLine)
	}
	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			recover()
		}()
		for !finished {
			time.Sleep(time.Millisecond * 1500)
			if dl.Stopped() {
				finished = true

				break
			}

			updateFunc(&dl)
		}
	}()
	go func() {
		complete <- dl.Download(destFile)
	}()
	err := <-complete

	finished = true

	wg.Wait()
	updateFunc(&dl)

	return dl, err
}
func (item *DownloadItem) Stop() {
	item.stopped = true
}
func (item *DownloadItem) Stopped() bool {
	return item.stopped
}
func (item *DownloadItem) finish() error {

	item.Status.Progress = 100
	item.Status.ETA = 0
	destFile := utils.RemoveExt(item.dst)
	if !utils.Exists(item.DownloadDirectory) {
		os.MkdirAll(item.DownloadDirectory, 0666)
	}
	os.Remove(destFile)
	return os.Rename(item.dst, destFile)
}
func (item *DownloadItem) Download(destFile string) error {
	item.dlLck.Lock()
	item.Downloading = true
	defer func() {
		item.Downloading = false
		item.dlLck.Unlock()
	}()
	item.stopped = false
	if destFile != "" {
		item.Name = utils.ReplaceInvalidFileChars(filepath.Base(destFile))
	}

	utils.WaitForConnection()
	h, err := GetHeaders(item.Link)
	if err == nil && h != nil {
		for item.Name == "" {
			item.Name = h.Name
		}
		if h.Size > 0 {
			item.Size = h.Size
		}
	}
	time.Sleep(time.Second)

	if item.DownloadDirectory == "" {
		item.DownloadDirectory = filepath.Dir(destFile)
	}
	item.dst = destFile + ".tmp"

	inf, err := os.Stat(item.dst)
	if err == nil {
		item.Status.Downloaded = inf.Size()
	}

	for !utils.InternetIsWorking() {

		if item.stopped {
			return nil
		}
		time.Sleep(time.Millisecond * 500)
	}
	var resp *http.Response = nil
	{
		destFile := filepath.Join(item.DownloadDirectory, utils.ReplaceInvalidFileChars(item.Name))
		destinfo, err := os.Stat(destFile)
		if err == nil {
			if destinfo.Size() == item.Size {
				item.Status.Progress = 100

				item.Status.ETA = 0
				os.Remove(item.dst)
				return nil
			}
		}

	}
	canResume := false
	if utils.Exists(item.dst) {
		if item.Status.Downloaded == item.Size {
			return item.finish()
		}
		if CanResume(item.Link) {
			canResume = true

			for i := 0; i < 4; i++ {
				for !utils.InternetIsWorking() {

					if item.stopped {
						return nil
					}
					time.Sleep(time.Millisecond * 500)
				}
				reqH := map[string]string{
					"Range": fmt.Sprintf("bytes=%d-", item.Status.Downloaded),
				}
				resp, err = utils.GetResponse(item.Link, &reqH)
				if err == nil {
					break
				}
			}

		}
	}

	if resp == nil {
		canResume = false
		item.Status.Downloaded = 0
		resp, err = utils.GetResponse(item.Link, nil)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	item.Size = resp.ContentLength
	var file *os.File
	var bytesDl int64 = 0

	var ln int64 = 0

	if !canResume {
		item.Status.Downloaded = 0
		file, err = os.OpenFile(item.dst, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	} else {
		file, err = os.OpenFile(item.dst, os.O_APPEND|os.O_WRONLY, 0644)
	}
	if err != nil {
		return err
	}

	start := time.Now()

	for {
		ln, err = io.CopyN(file, resp.Body, 2048)
		bytesDl += int64(ln)
		if item.stopped {

			return nil
		}
		item.Status.Downloaded += int64(ln)
		if err == io.EOF {
			file.Close()
			return item.finish()
		}
		if err != nil {
			file.Close()
			return err
		}
		if time.Since(start).Milliseconds() > 1000 {
			if bytesDl > 0 {
				t := float64(time.Since(start).Milliseconds()) / 1000
				rt := float64(bytesDl) / t
				if item.Size > 0 {
					rem := float64(item.Size-item.Status.Downloaded) / rt
					dur, err := time.ParseDuration(fmt.Sprintf("%ds", int64(rem)))
					if err == nil {
						item.Status.ETA = dur
					}
				}
				bytesDl = 0
				item.Status.Rate = utils.FormatBytes(int64(rt)) + "/S"
				if item.Size > 0 && item.Status.Downloaded > 0 {
					item.Status.Progress = int((item.Status.Downloaded * 100) / item.Size)
				}

			}

			start = time.Now()
		}
	}
}
