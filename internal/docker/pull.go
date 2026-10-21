package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// Progress describes how far a long step is. Percent is 0..1, or -1 when unknown.
type Progress struct {
	Status  string
	Percent float64
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ensureImage pulls ref unless it already exists locally.
func (c *Client) ensureImage(ctx context.Context, ref string, report func(Progress)) error {
	if _, err := c.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !client.IsErrNotFound(err) {
		return err
	}

	report(Progress{Status: "Pulling " + ref + "…", Percent: 0})
	rc, err := c.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	return readPull(rc, ref, report)
}

type pullMessage struct {
	Status         string `json:"status"`
	ID             string `json:"id"`
	Error          string `json:"error"`
	ProgressDetail struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
}

// readPull follows Docker's pull stream and reports one overall percentage.
func readPull(r io.Reader, ref string, report func(Progress)) error {
	type layer struct{ cur, total int64 }
	layers := map[string]*layer{}
	dec := json.NewDecoder(r)
	for {
		var m pullMessage
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if m.Error != "" {
			return errors.New(m.Error)
		}
		if m.ID != "" && m.ProgressDetail.Total > 0 {
			l := layers[m.ID]
			if l == nil {
				l = &layer{}
				layers[m.ID] = l
			}
			l.cur, l.total = m.ProgressDetail.Current, m.ProgressDetail.Total
		}
		var cur, total int64
		for _, l := range layers {
			cur += l.cur
			total += l.total
		}
		pct := 0.0
		if total > 0 {
			pct = float64(cur) / float64(total)
		}
		report(Progress{Status: fmt.Sprintf("Pulling %s…", ref), Percent: pct})
	}
}
