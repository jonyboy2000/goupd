package goupd

import (
	"compress/bzip2"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var autoUpdateLock sync.Mutex

// SignalVersion is called when seeing another peer running the same software
// to notify of its version. This will check if the peer is updated compared
// to us, and call RunAutoUpdateCheck() if necessary
func SignalVersion(git, build string) {
	if git == "" {
		return
	}
	if git == GIT_TAG {
		return
	}

	// compare build
	if build <= DATE_TAG {
		return // we are more recent (or equal)
	}

	// perform check
	go RunAutoUpdateCheck()
}

// RunAutoUpdateCheck will perform the update check, update the executable and
// return false if no update was performed. In case of update the program
// should restart and RunAutoUpdateCheck() should not return, but if it does,
// it'll return true.
func RunAutoUpdateCheck() bool {
	autoUpdateLock.Lock()
	defer autoUpdateLock.Unlock()

	// get latest version
	if PROJECT_NAME == "unconfigured" {
		log.Println("[goupd] Auto-updater failed to run, project not properly configured")
		return false
	}
	resp, err := http.Get(HOST + PROJECT_NAME + "/LATEST")
	if err != nil {
		log.Printf("[goupd] Auto-updater failed to run: %s", err)
		return false
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[goupd] Auto-updater failed to read latest version: %s", err)
		return false
	}

	updInfo := strings.SplitN(strings.TrimSpace(string(body)), " ", 3)
	if len(updInfo) != 3 {
		log.Printf("[goupd] Auto-updater failed to parse update data (%s)", body)
		return false
	}

	if updInfo[1] == GIT_TAG {
		log.Printf("[goupd] Current version is up to date (%s)", GIT_TAG)
		return false
	}

	log.Printf("[goupd] New version found %s/%s (current: %s/%s) - downloading...", updInfo[0], updInfo[1], DATE_TAG, GIT_TAG)

	updPrefix := updInfo[2]

	// check if compatible version is available
	resp, err = http.Get(HOST + PROJECT_NAME + "/" + updPrefix + ".arch")
	if err != nil {
		log.Printf("[goupd] Auto-updater failed to get arch info: %s", err)
		return false
	}
	defer resp.Body.Close()

	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[goupd] Auto-updater failed to read arch info: %s", err)
		return false
	}

	found := false
	myself := runtime.GOOS + "_" + runtime.GOARCH

	for _, arch := range strings.Split(strings.TrimSpace(string(body)), " ") {
		if arch == myself {
			found = true
			break
		}
	}

	if !found {
		log.Printf("[goupd] Auto-updater unable to run, no version available for %s", myself)
		return false
	}

	// download actual update
	resp, err = http.Get(HOST + PROJECT_NAME + "/" + updPrefix + "/" + PROJECT_NAME + "_" + myself + ".bz2")
	if err != nil {
		log.Printf("[goupd] Auto-updater failed to get update: %s", err)
		return false
	}
	defer resp.Body.Close()

	stream := bzip2.NewReader(resp.Body)

	err = installUpdate(stream)

	if err != nil {
		log.Printf("[goupd] Auto-updater failed to install update: %s", err)
		return false
	} else {
		log.Printf("[goupd] Program upgraded, restarting")
		restartProgram()
		return true
	}
}

func installUpdate(r io.Reader) error {
	// install updated file (in io.Reader)
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	// decompose executable
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)

	// copy data in new file
	newPath := filepath.Join(dir, "."+name+".new")
	fp, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer fp.Close()

	_, err = io.Copy(fp, r)
	if err != nil {
		return err
	}
	fp.Close()

	// move files
	oldPath := filepath.Join(dir, "."+name+".old")

	err = os.Rename(exe, oldPath)
	if err != nil {
		return err
	}

	err = os.Rename(newPath, exe)
	if err != nil {
		// rename failed, revert previous rename (hopefully successful)
		os.Rename(oldPath, exe)
		return err
	}

	// attempt to remove old
	err = os.Remove(oldPath)
	if err != nil {
		// hide it since remove failed
		hideFile(oldPath)
	}

	return nil
}
