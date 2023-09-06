package downloader

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var lck sync.Mutex

type Timespan time.Duration

func (t Timespan) Format(format string) string {
	z := time.Unix(0, 0).UTC()
	return z.Add(time.Duration(t)).Format(format)
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
			res, err := utils.GetResponse(uri, hd)
			if err == nil {
				defer res.Body.Close()
				sz[idx] = res.ContentLength
			}

		}(i)
	}
	wg.Wait()
	return sz[0] > 0 && sz[1] > 0 && sz[0] != sz[1]

}

func Download(link string) error {

	defer func() {
		recover()
	}()

	complete := make(chan error)
	finished := false

	defer close(complete)
	wg := sync.WaitGroup{}
	updateFunc := func(download *downloader.DownloadItem, dl *models.Media) {
		name := download.Name
		if name != "" {
			dl.Name = name
		}
		dl.ETA = Timespan(download.Status.ETA).Format("15:04:05")
		dl.Rate = download.Status.Rate
		dl.Downloaded = download.Status.Downloaded
		if download.Size > 0 {
			dl.Progress = uint((download.Status.Downloaded * 100) / download.Size)
			dl.Size = download.Size
		}
		if download.Downloading && dl.State != models.DELETING || dl.State != models.STOPPED {
			dl.State = models.DOWNLOADING
		} else if download.Stopped() && dl.State != models.DELETING {
			dl.State = models.STOPPED
		}
		SaveDownload(*dl)
	}
	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			recover()
		}()
		for !finished {
			time.Sleep(time.Millisecond * 1500)
			if dl.State == models.DELETING {
				finished = true
				download.Stop()
				break
			} else if dl.State == models.STOPPED {
				finished = true
				download.Stop()
			}

			updateFunc(download, dl)
		}
	}()
	go func() {
		complete <- download.Download()
	}()
	err = <-complete
	if dl.State != models.DELETING || dl.State != models.STOPPED {
		if err == nil {
			dl.State = models.FINISHED
		} else {
			dl.State = models.ERROR
		}
	}
	finished = true

	wg.Wait()
	updateFunc(download, dl)

	return err
}

func (item *DownloadItem) Download() error {
	item.dlLck.Lock()
	item.Downloading = true
	defer func() {
		item.Downloading = false
		item.dlLck.Unlock()
	}()
	item.stopped = false
	for item.Name == "" {
		utils.WaitForConnection()
		h, err := GetHeaders(item.Link)
		if err == nil && h != nil {
			item.Name = h.Name
			if h.Size > 0 {
				item.Size = h.Size
			}
		}
		time.Sleep(time.Second)
	}
	if item.DownloadDirectory == "" {
		item.DownloadDirectory = GetDefaultDownloadDir()
	}
	item.dst = filepath.Join(GetTempDir(), item.Name+tmpExt)

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
				resp, err = utils.GetResponse(item.Link, &utils.Headers{
					map[string]string{
						"Range": fmt.Sprintf("bytes=%d-", item.Status.Downloaded),
					},
				})
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
	{
		tmpDir := filepath.Dir(item.dst)
		if !utils.Exists(tmpDir) {
			os.MkdirAll(tmpDir, 0655)
		}
	}
	if !canResume {
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

			utils.Log(fmt.Sprintf("Downloaded (%s) %s/%s ETA %f s", item.Name, utils.FormatBytes(item.Status.Downloaded), utils.FormatBytes(item.Size), item.Status.ETA.Seconds()))
			start = time.Now()
		}
	}
}

var dirLock = sync.Mutex{}
