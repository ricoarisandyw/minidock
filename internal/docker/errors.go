package docker

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var portRe = regexp.MustCompile(`(?i)(?:port is already allocated|address already in use|bind for [^ ]*:(\d+))`)

// Explain turns a raw Docker error into a short, friendly sentence.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Docker took too long to answer. Is it still starting up?"
	case strings.Contains(low, "cannot connect to the docker daemon"),
		strings.Contains(low, "is the docker daemon running"),
		strings.Contains(low, "connection refused"),
		strings.Contains(low, "no such file or directory") && strings.Contains(low, "docker.sock"):
		return "Docker is not running. Start Docker Desktop, then try again."
	case portRe.MatchString(msg):
		return "That port is already in use. Pick another port and try again."
	case strings.Contains(low, "is already in use by container"), strings.Contains(low, "conflict. the container name"):
		return "A container with that name already exists. Choose a different name."
	case strings.Contains(low, "pull access denied"), strings.Contains(low, "repository does not exist"),
		strings.Contains(low, "manifest unknown"), strings.Contains(low, "not found: manifest"):
		return "Image or version not found. Check the image name and version in the template."
	case strings.Contains(low, "no space left"):
		return "Docker has no disk space left. Free some space (docker system prune) and retry."
	case strings.Contains(low, "toomanyrequests"), strings.Contains(low, "rate limit"):
		return "Docker Hub rate limit reached. Wait a bit or log in with `docker login`."
	case strings.Contains(low, "no such container"):
		return "That container no longer exists."
	}
	// Keep unknown errors readable: the SDK prefixes with "Error response from daemon: ".
	return strings.TrimSpace(strings.TrimPrefix(msg, "Error response from daemon:"))
}
