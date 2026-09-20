package aws

import "strconv"

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
