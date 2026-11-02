package httpapi

import (
	"net/http"
	"strconv"
)

func pageNumber(w http.ResponseWriter, r *http.Request, name string) (int64, bool, bool) {
	values, present := r.URL.Query()[name]
	if !present {
		return 0, false, true
	}
	if len(values) == 1 {
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err == nil && value >= 0 {
			return value, true, true
		}
	}
	writeError(w, 400, "invalid_page", "Use a nonnegative integer for "+name+".")
	return 0, present, false
}
