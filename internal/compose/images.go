package compose

import (
	"context"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// Image is a Docker image that a Compose project built.
type Image struct {
	ID   string `json:"id"`
	Name string `json:"name"` // repository:tag, "<none>:<none>" when untagged
}

// ProjectImages lists the images that Compose built for a project: the ones
// it labelled with the project's name, and the ones named <project>-<service>
// as Compose names them. Pulled images (postgres, redis...) are not listed.
func (Compose) ProjectImages(ctx context.Context, project string) ([]Image, error) {
	if project == "" || strings.HasPrefix(project, "-") {
		return nil, nil
	}
	seen := map[string]bool{}
	var list []Image
	for _, filter := range []string{"label=com.docker.compose.project=" + project, "reference=" + project + "-*"} {
		out, err := execx.Run(ctx, "", "docker", "image", "ls", "--filter", filter, "--format", "{{.ID}} {{.Repository}}:{{.Tag}}")
		if err != nil {
			return nil, err
		}
		for _, img := range ParseImages(out) {
			if !seen[img.ID] {
				seen[img.ID] = true
				list = append(list, img)
			}
		}
	}
	return list, nil
}

// ParseImages parses `docker image ls --format '{{.ID}} {{.Repository}}:{{.Tag}}'`.
func ParseImages(out []byte) []Image {
	var list []Image
	for _, line := range strings.Split(string(out), "\n") {
		id, name, _ := strings.Cut(strings.TrimSpace(line), " ")
		if id != "" {
			list = append(list, Image{ID: id, Name: strings.TrimSpace(name)})
		}
	}
	return list
}

// RemoveImage removes an image without --force: Docker refuses one that a
// container still uses.
func (Compose) RemoveImage(ctx context.Context, id string) error {
	_, err := execx.RunChange(ctx, "", "docker", "image", "rm", "--", id)
	return err
}
