//go:build unix

package launcher

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

func childMain(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "exit":
			code, err := strconv.Atoi(args[1])
			if err != nil {
				return 2
			}
			return code
		case "echo-streams":
			if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
				return 2
			}
			fmt.Fprintln(os.Stderr, "child stderr")
			return 0
		case "sleep":
			fmt.Printf(`{"pid":%d}`+"\n", os.Getpid())
			time.Sleep(time.Hour)
			return 0
		}
	}
	report := map[string]any{
		"args": args,
		"env":  childEnvironment(),
		"cwd":  childWorkingDirectory(),
		"pid":  os.Getpid(),
	}
	data, err := json.Marshal(report)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if _, err := os.Stdout.Write(data); err != nil {
		return 2
	}
	return 0
}

func childEnvironment() map[string]string {
	env := map[string]string{}
	for _, pair := range os.Environ() {
		name, value, found := strings.Cut(pair, "=")
		if !found {
			value = ""
		}
		env[name] = value
	}
	return env
}

func childWorkingDirectory() string {
	directory, err := os.Getwd()
	if err != nil {
		return "error: " + err.Error()
	}
	return directory
}
