package config

import (
	"bufio"
	"os"
	"strings"
)

// Load returns entries declared by path.
func Load(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 1 && fields[0] == "entry" {
			entries = append(entries, strings.Join(fields[1:], " "))
		}
	}
	return entries, scanner.Err()
}
