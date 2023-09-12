package utils

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = kernel32.NewProc("CreateMutexW")
	user32          = syscall.MustLoadDLL("user32.dll")
	MAX_FILE_SIZE   = int64(1024) * 1024 * 50
	USERAGENT       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/93.0.4573.0 Safari/537.36"
)

// var BaseUri = "http://localhost:3000"
var MUTEX = "libgendownloader"

func GetResponse(uri string, headers *map[string]string) (*http.Response, error) {
	client := &http.Client{
		Jar: http.DefaultClient.Jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {

			// Go's http.DefaultClient allows 10 redirects before returning an
			// an error. We have mimicked this default behavior.s
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}
	useragentset := false
	if headers != nil {
		for key, val := range *headers {
			req.Header.Set(key, val)
			if strings.EqualFold(key, "User-Agent") {
				useragentset = true
			}
		}
	}
	if !useragentset {
		req.Header.Set("User-Agent", USERAGENT)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		defer resp.Body.Close()
		return nil, fmt.Errorf("received status code %d", resp.StatusCode)
	}
	return resp, nil
}

func ReplaceInvalidFileChars(file string) string {
	chars := `[\\/:*?""<>|]`

	rgx := regexp.MustCompile(chars)
	return rgx.ReplaceAllString(file, "_")

}

var assetLck = sync.Mutex{}

func GetAssetDir() string {
	assetLck.Lock()
	defer assetLck.Unlock()

	dir := filepath.Join(GetBaseDirectory(), "asset")
	if !Exists(dir) {
		os.MkdirAll(dir, 0655)
		SetHidden(dir)
	}
	return dir
}

func CreateMutex(name string) (uintptr, error) {
	ret, _, err := procCreateMutex.Call(
		0,
		0,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(name))),
	)
	switch int(err.(syscall.Errno)) {
	case 0:
		return ret, nil
	default:
		return ret, err
	}
}
func FirstInstance() bool {
	_, err := CreateMutex(MUTEX)
	return err == nil
}
func MoveOrCopyFile(src, dest string) error {
	err := os.Rename(src, dest)
	if err == nil {
		return nil
	}
	err = CopyFile(src, dest)
	if err == nil {
		os.Remove(src)
	}

	return err
}

func GetFileSize(src string) int64 {
	size := int64(0)
	stat, err := os.Stat(src)
	if err == nil {
		return stat.Size()
	}
	return size

}
func CopyFile(src, dest string) error {

	var err error
	if !Exists(src) {
		return errors.New("file does not exist")
	}
	stat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !Exists(filepath.Dir(dest)) {
		err = os.MkdirAll(dest, 0777)
		if err != nil {
			return err
		}
	}
	sstream, err := os.OpenFile(src, os.O_RDWR, 0777)
	if err != nil {
		return err
	}
	defer sstream.Close()
	if Exists(dest) {
		os.Remove(dest)
	}
	dstream, err := os.OpenFile(dest, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer dstream.Close()

	c, err := io.CopyN(dstream, sstream, stat.Size())
	if err != nil || c != stat.Size() {
		return errors.New("failed to copy")
	}

	return err
}

var dlDir sync.Mutex

func FormatBytes(bytes int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}

	var i int
	value := float64(bytes)

	for value > 1024 && i < len(units) {
		value /= 1024
		i++
	}
	return fmt.Sprintf("%.2f %s", value, units[i])
}
func RemoveEmptyFromSlice(src []string) []string {
	final := make([]string, 0, 20)
	for _, el := range src {
		el = strings.TrimSpace(el)
		if len(el) > 0 {
			final = append(final, el)
		}

	}
	return final
}
func ExecuteProcess(program string, args ...string) (string, error) {
	cmd := exec.Command(program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil

}
func OpenBrowser(url string) error {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		return err
	}
	return nil

}
func RemoveExt(path string) string {
	base := path
	ext := filepath.Ext(path)
	if len(ext) > 0 {
		base = base[:len(base)-len(ext)]
	}
	return base
}

func KillFile(file string) {
	base := filepath.Base(file)
	cmd := exec.Command("taskkill", "/IM", base, "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Run()
}
func KillByPID(pid int) {
	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Run()
}

type Info struct {
	FullPath string
	Info     os.FileInfo
}

func GetInfosFromDir(dir string) []Info {
	infos := make([]Info, 0, 200)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil {
			infos = append(infos, Info{
				FullPath: path,
				Info:     info,
			})
		}
		return err
	})
	return infos
}
func DeleteAllFiles(dir string) {
	killWg := sync.WaitGroup{}
	hasExecs := false
	infs := GetInfosFromDir(dir)

	for _, inf := range infs {
		hasExecs = true
		if !inf.Info.IsDir() && strings.EqualFold(filepath.Ext(inf.Info.Name()), ".exe") {
			hasExecs = true
			break
		}
	}
	for _, inf := range infs {
		if !inf.Info.IsDir() && strings.EqualFold(filepath.Ext(inf.Info.Name()), ".exe") {
			killWg.Add(1)
			go func(f string) {
				defer killWg.Done()
				KillFile(f)
				time.Sleep(time.Millisecond * 200)
			}(inf.Info.Name())
		}
	}
	if hasExecs {
		killWg.Wait()
	}
	for _, inf := range infs {
		if !inf.Info.IsDir() {
			os.Remove(inf.FullPath)
		}
	}
	for _, inf := range infs {
		if inf.Info.IsDir() {
			os.RemoveAll(inf.FullPath)
		}
	}
}

func GetDownloadsDir() string {
	dlDir.Lock()
	defer dlDir.Unlock()
	dl := filepath.Join(GetBaseDirectory(), "downloads")
	if !Exists(dl) {
		os.MkdirAll(dl, 0755)
	}
	return dl
}
func GetData(address string) ([]byte, error) {
	WaitForConnection()
	res, err := http.Get(address)
	errCount := 0
	for err != nil {
		errCount++
		WaitForConnection()
		res, err = http.Get(address)
		if errCount > 5 {
			return []byte{}, err
		}
		time.Sleep(1500 * time.Millisecond)
	}
	defer res.Body.Close()
	if !(res.StatusCode >= 200 && res.StatusCode < 300) {
		return []byte{}, fmt.Errorf("status code error: %d %s", res.StatusCode, res.Status)
	}
	htmlbytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return []byte{}, err
	}
	return htmlbytes, nil
}
func GetBaseDirectory() string {

	exe, _ := os.Executable()
	return filepath.Dir(exe)
}
func InternetIsWorking() bool {
	_, err := http.Get("http://clients3.google.com/generate_204")
	return err == nil
}
func WaitForConnection() {

	for !InternetIsWorking() {
		time.Sleep(time.Millisecond * 1500)
	}

}

func ParseCommandline(line string) []string {
	line = strings.TrimSpace(line)
	arr := make([]string, 0, 10)
	quote := false
	spaced := false

	if line[0] == byte('"') {
		quote = true
		line = line[1:]
	} else {
		spaced = true
	}
	if !strings.Contains(line, "\"") {
		rgx := regexp.MustCompile(`\s{2,}`)
		line = rgx.ReplaceAllString(line, "")
	}
	buffer := ""
	for i := 0; i < len(line); i++ {
		c := string(line[i])

		if quote {
			if c == "\"" {
				quote = false
				buffer = strings.TrimSpace(buffer)
				if len(buffer) > 0 {
					arr = append(arr, buffer)
					buffer = ""
				}
			} else {
				buffer = buffer + c
			}
		} else if spaced {
			if c == " " {
				spaced = false
				buffer = strings.TrimSpace(buffer)
				if len(buffer) > 0 {
					arr = append(arr, buffer)
					buffer = ""
				}
			} else {
				buffer = buffer + c
			}
		} else {
			if c == " " {
				spaced = true
				buffer = strings.TrimSpace(buffer)
				if len(buffer) > 0 {
					arr = append(arr, buffer)
					buffer = ""

				}
			} else if c == "\"" {
				quote = true
				buffer = strings.TrimSpace(buffer)
				if len(buffer) > 0 {
					arr = append(arr, buffer)
					buffer = ""

				}
			} else {
				buffer = buffer + c
			}
		}

	}

	buffer = strings.TrimSpace(buffer)
	if len(buffer) > 0 {
		arr = append(arr, buffer)
	}

	return arr
}

func getUniqueFileName(file string, num int) string {
	ext := filepath.Ext(file)
	name := RemoveExt(filepath.Base(file))
	dir := filepath.Dir(file)

	p := filepath.Join(dir, name+fmt.Sprintf("(%d)%s", num, ext))
	if Exists(p) {
		return getUniqueFileName(file, num+1)
	}
	return p
}
func GetUniqueFileName(file string) string {
	if Exists(file) {
		return getUniqueFileName(file, 2)
	}
	return file
}
func Exists(file string) bool {

	if _, err := os.Stat(file); err != nil {
		if os.IsNotExist(err) {

			return false
		}
	}

	return true
}
func WriteFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	if err != nil {
		for i := 0; i < 10; i++ {
			_, err = f.Write(data)
			if err == nil {
				break
			}
			time.Sleep(time.Millisecond * 200)
		}
	}
	return err
}
func AppendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
func ReadFile(path string) ([]byte, error) {
	if Exists(path) {
		f, err := os.OpenFile(path, os.O_RDONLY, 0755)
		if err == nil {
			defer f.Close()
			data, err := io.ReadAll(f)
			if err == nil {

				return data, nil
			} else {
				return []byte{}, err
			}
		} else {
			return []byte{}, err
		}
	}
	return []byte{}, errors.New("file does not exist")
}

func RemoveIndex(s []string, index int) []string {
	if (index) >= len(s) {
		return s
	} else if index == 0 && len(s) == 1 {
		return make([]string, 0, 1)
	} else if index == 0 {
		return s[1:]
	}
	return append(s[:index], s[index+1:]...)
}
func InSlice(list []string, search string, strict bool) bool {
	search = strings.TrimSpace(search)
	for _, s := range list {
		if strict {
			if strings.EqualFold(strings.TrimSpace(s), search) {
				return true
			}
		} else {
			if strings.TrimSpace(s) == search {
				return true
			}
		}
	}
	return false
}

func ExecCommandExists(file string) bool {
	if Exists(file) {
		return true
	}
	cmd := exec.Command("where", file)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	res, err := cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(res), "\n") {
			if Exists(strings.TrimSpace(line)) {
				return true
			}
		}
	}
	return false
}

func SetHidden(path string) error {
	filenameW, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	err = syscall.SetFileAttributes(filenameW, syscall.FILE_ATTRIBUTE_HIDDEN)
	if err != nil {
		return err
	}

	return nil
}
func RemoveFromSlice(lst []string, search string) bool {
	for i := 0; i < len(lst); i++ {
		if lst[i] == search {
			RemoveIndex(lst, i)
			return true
		}
	}
	return false
}

func SliceEquals(first []string, second []string) bool {
	if first == nil && second == nil {
		return true
	}
	if first == nil || second == nil {
		return false
	}

	if len(first) != len(second) {
		return false
	}
	for i := 0; i < len(first); i++ {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}
func GetInstallPath(file string) string {
	baseDir := filepath.Dir(GetBaseDirectory())
	base := RemoveExt(filepath.Base(file))
	installpath := filepath.Join(baseDir, base)

	return installpath
}

var lauchLck sync.Mutex
