package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/polyverse/masche/memaccess"
	"github.com/polyverse/ropoly/lib"
	"github.com/polyverse/ropoly/lib/types"
	log "github.com/sirupsen/logrus"
)

const indent string = "    "

const defaultStart uint64 = 0
const defaultEnd uint64 = 0x7fffffffffffffff

type DirectoryListingEntryType string

const (
	EntryTypeDir  DirectoryListingEntryType = "Directory"
	EntryTypeFile DirectoryListingEntryType = "File"
)

type DirectoryListingEntry struct {
	Path            string                    `json:"path"`
	Type            DirectoryListingEntryType `json:"type"`
	PolyverseTained bool                      `json:"polyverseTainted"`
}

func logErrors(hardError error, softErrors []error) {
	if hardError != nil {
		//log.Fatal(hardError)
		log.Print(hardError)
	}

	for _, softError := range softErrors {
		log.Print(softError)
	}
} // logErrors

func HealthHandler(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode("Ropoly API Healthy")
} // ROPTestHandler()

func getFilepath(r *http.Request, uri string) string {
	splitUri := strings.Split(r.RequestURI, uri)
	path := strings.SplitN(splitUri[len(splitUri)-1], "?", 2)[0]
	if path == "" {
		path = "/"
	}
	return NormalizePath(path)
}

func FileHandler(w http.ResponseWriter, r *http.Request) {
	path := getFilepath(r, "api/v1/files")

	fi, err := os.Stat(path)
	if err != nil {
		log.WithError(err).Warningf("Unable to stat path %s. Not handling it like a directory.", path)
	} else if fi.IsDir() {
		DirectoryListingHandler(w, r, FileSystemRoot + path)
		return
	}

	query := r.FormValue("query")
	switch query {
	case "taints":
		PolyverseTaintedFileHandler(w, r, path)
	case "disasm":
		FileDisasmHandler(w, r, path)
	case "gadgets":
		GadgetsFromFileHandler(w, r, path)
	case "fingerprint":
		FingerprintForFileHandler(w, r, path)
	case "search":
		FileGadgetSearchHandler(w, r, path)
	default:
		PolyverseTaintedFileHandler(w, r, path)
	} // switch
}

func PidHandler(w http.ResponseWriter, r *http.Request) {
	pid, err := getPid(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	query := r.FormValue("query")
	switch query {
	case "taints":
		PolyverseTaintedPidHandler(w, r, int(pid))
	case "disasm":
		ProcessDisasmHandler(w, r, int(pid))
	case "gadgets":
		GadgetsFromPidHandler(w, r, int(pid))
	case "fingerprint":
		FingerprintForPidHandler(w, r, int(pid))
	case "search":
		PidGadgetSearchHandler(w, r, int(pid))
	case "regions":
		ROPMemoryRegionsHandler(w, r)
	case "region-fingerprints":
		RegionFingerprintsHandler(w, r, int(pid))
	default:
		PolyverseTaintedPidHandler(w, r, int(pid))
	}
}

func DirectoryListingHandler(w http.ResponseWriter, r *http.Request, dirpath string) {
	listing := []*DirectoryListingEntry{}

	err := filepath.Walk(dirpath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.WithError(err).Error("Unable to walk filesystem path %s", path)
			return nil
		}
		entry := &DirectoryListingEntry{
			Path: path,
		}
		if info.IsDir() {
			entry.Type = EntryTypeDir
		} else {
			entry.Type = EntryTypeFile
			pvTaint, err := lib.HasPolyverseTaint(path)
			if err != nil {
				log.WithError(err).Errorf("Error when checking for Polyverse taint on path %s", path)
			} else {
				entry.PolyverseTained = pvTaint
			}
		}
		listing = append(listing, entry)

		if info.IsDir() && dirpath != path {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		logErrors(err, make([]error, 0))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	b, err := json.MarshalIndent(&listing, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if

	w.Write(b)
}

func PolyverseTaintedFileHandler(w http.ResponseWriter, r *http.Request, path string) {
	signatureResult, err := lib.HasPolyverseTaint(path)
	if err != nil {
		logErrors(err, make([]error, 0))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	b, err := json.MarshalIndent(&signatureResult, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if
	w.Write(b)
}

func GadgetsFromFileHandler(w http.ResponseWriter, r *http.Request, path string) {
	var gadgetLen uint64 = 2 // Gadgets longer than 2 instructions must be requested explicitly
	var err error
	lenStr := r.Form.Get("len")
	if lenStr != "" {
		gadgetLen, err = strconv.ParseUint(lenStr, 0, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} // if
	} // if

	gadgets, harderror, softerrors := lib.GadgetsFromFile(path, int(gadgetLen))
	logErrors(harderror, softerrors)
	if harderror != nil {
		http.Error(w, harderror.Error(), http.StatusInternalServerError)
		return
	} // if

	b, err := json.MarshalIndent(gadgets, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if
	w.Write(b)
}

func FileDisasmHandler(w http.ResponseWriter, r *http.Request, path string) {
	var start uint64 = defaultStart
	startStr := r.Form.Get("start")
	if startStr != "" {
		var err error
		start, err = strconv.ParseUint(startStr, 0, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} // if
	} // if

	var end uint64 = defaultEnd
	endStr := r.Form.Get("end")
	if endStr != "" {
		var err error
		end, err = strconv.ParseUint(endStr, 0, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} // if

	instructions, harderror, softerrors := lib.DisassembleFile(path, types.Addr(start), types.Addr(end))
	logErrors(harderror, softerrors)
	if harderror != nil {
		http.Error(w, harderror.Error(), http.StatusInternalServerError)
		return
	} // if

	b, err := json.MarshalIndent(instructions, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if
	w.Write(b)
}

func ProcessDisasmHandler(w http.ResponseWriter, r *http.Request, pid int) {
	var start uint64 = defaultStart
	startStr := r.Form.Get("start")
	if startStr != "" {
		var err error
		start, err = strconv.ParseUint(startStr, 0, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} // if
	} // if

	var end uint64 = defaultEnd
	endStr := r.Form.Get("end")
	if endStr != "" {
		var err error
		end, err = strconv.ParseUint(endStr, 0, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} // if

	instructions, harderror, softerrors := lib.DisassembleProcess(pid, types.Addr(start), types.Addr(end))
	logErrors(harderror, softerrors)
	if harderror != nil {
		http.Error(w, harderror.Error(), http.StatusInternalServerError)
		return
	} // if

	b, err := json.MarshalIndent(instructions, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if
	w.Write(b)
}

func FileGadgetSearchHandler(w http.ResponseWriter, r *http.Request, path string) {
	search := r.Form.Get("string")
	if search == "" {
		search = r.Form.Get("regexp")
		if search == "" {
			err := errors.New("Search with no or empty target given.")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} // if

	http.Error(w, "This functionality is not yet implemented.", http.StatusNotImplemented)
}

func PidListingHandler(w http.ResponseWriter, r *http.Request) {
	pIdsResult, harderror, softerrors := lib.GetAllPids()
	logErrors(harderror, softerrors)
	if harderror != nil {
		http.Error(w, harderror.Error(), http.StatusInternalServerError)
		return
	} // if

	b, err := json.MarshalIndent(&pIdsResult, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if
	w.Write(b)
} // ROPPIdsHandler()

func getPid(r *http.Request) (uint64, error) {
	var err error

	var pidN uint64 = uint64(os.Getpid())
	pid := mux.Vars(r)["pid"]
	if (pid != "") && (pid != "0") {
		pidN, err = strconv.ParseUint(pid, 0, 64)
		if err != nil {
			err = errors.New("Cannot parse PID.")
		}
	}
	return pidN, err
}

func GadgetsFromPidHandler(w http.ResponseWriter, r *http.Request, pid int) {
	var err error

	var gadgetLen uint64 = 2 // Gadgets longer than 2 instructions must be requested explicitly
	lenStr := r.Form.Get("len")
	if lenStr != "" {
		gadgetLen, err = strconv.ParseUint(lenStr, 0, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} // if
	} // else if

	var start uint64 = defaultStart
	startStr := r.Form.Get("start")
	if startStr != "" {
		start, err = strconv.ParseUint(startStr, 0, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} // if
	} // else if

	var end uint64 = defaultEnd
	endStr := r.Form.Get("end")
	if endStr != "" {
		end, err = strconv.ParseUint(endStr, 0, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} // if
	} // else if

	var base uint64 = defaultStart
	baseStr := r.Form.Get("base")
	if baseStr != "" {
		base, err = strconv.ParseUint(baseStr, 0, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} // if
	} // else if

	gadgets, harderror, softerrors := lib.GadgetsFromProcess(pid, int(gadgetLen),
		types.Addr(start), types.Addr(end), types.Addr(base))
	logErrors(harderror, softerrors)
	if err != nil {
		logErrors(err, softerrors)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if

	b, err := json.MarshalIndent(gadgets, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func PolyverseTaintedPidHandler(w http.ResponseWriter, r *http.Request, pid int) {
	libraries, err, softerrors := lib.GetLibrariesForPid(pid, true)
	if err != nil {
		logErrors(err, softerrors)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	b, err := json.MarshalIndent(libraries, "", indent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} // if
	w.Write(b)
}

func PidGadgetSearchHandler(w http.ResponseWriter, r *http.Request, pid int) {
	search := r.Form.Get("string")
	if search == "" {
		search = r.Form.Get("regexp")
		if search == "" {
			err := errors.New("Search with no or empty target given.")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} // if

	http.Error(w, "This functionality is not yet implemented.", http.StatusNotImplemented)
}

func ROPMemoryRegionsHandler(w http.ResponseWriter, r *http.Request) {
	var pidSelf = uint64(os.Getpid())
	var err error

	pidN := pidSelf
	pid := mux.Vars(r)["pid"]
	if (pid != "") && (pid != "0") {
		pidN, err = strconv.ParseUint(pid, 0, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		} // if
	} // if

	var access memaccess.Access = memaccess.None

	accessS := strings.ToUpper(r.FormValue("access"))
	if accessS == "NONE" {
		access = memaccess.None
	} else if accessS == "" {
		access = memaccess.Readable
	} else {
		if i := strings.Index(accessS, "R"); i != -1 {
			access |= memaccess.Readable
			accessS = strings.Replace(accessS, "R", "", 1)
		} // if
		if i := strings.Index(accessS, "W"); i != -1 {
			access |= memaccess.Writable
			accessS = strings.Replace(accessS, "W", "", 1)
		} // if
		if i := strings.Index(accessS, "X"); i != -1 {
			access |= memaccess.Executable
			accessS = strings.Replace(accessS, "X", "", 1)
		} // if
		if i := strings.Index(accessS, "F"); i != -1 {
			access |= memaccess.Free
			accessS = strings.Replace(accessS, "F", "", 1)
		} // if
		if accessS != "" {
			http.Error(w, "Improper Access specification.", http.StatusBadRequest)
			return
		} // if
	} // else

	regionsResult, harderror, softerrors := lib.ROPMemoryRegions(int(pidN), access)
	logErrors(harderror, softerrors)
	if harderror != nil {
		http.Error(w, harderror.Error(), http.StatusBadRequest)
		return
	} // if

	b, err := json.MarshalIndent(&regionsResult, "", "    ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} // if
	w.Write(b)
} // ROPMemoryRegionsHandler()