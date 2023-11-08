package collector

import "strconv"

func parseFloat(str string) (float64, error) {
	if len(str) > 0 {
		return strconv.ParseFloat(str, 64)
	}
	return 0, nil
}
