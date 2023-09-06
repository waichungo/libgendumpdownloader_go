package mimes

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed mime-types.json
var mimefile []byte
var mimeObj map[string]string = map[string]string{}
var lck = sync.Mutex{}

func init() {
	lck.Lock()
	defer lck.Unlock()
	if len(mimeObj) == 0 {
		json.Unmarshal(mimefile, &mimeObj)
	}

}
func GetExtensionForMime(mimetype string) string {

	arr := strings.Split(mimetype, ";")
	res, e := mimeObj[strings.TrimSpace(arr[0])]
	if e {
		arr := strings.Split(res, ",")
		return arr[0]
	}
	return "bin"
}
