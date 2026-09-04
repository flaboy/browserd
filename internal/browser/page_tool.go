package browser

import (
	"fmt"
	"strings"

	browserdclient "github.com/flaboy/browserd-client-go/pkg/browserd"
)

func (s *Service) PageTool(_ string, input browserdclient.PageToolInput) (browserdclient.PageToolResult, error) {
	if err := browserdclient.ValidatePageToolInput(input); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: unsupported pageTool method %s", ErrActionFailed, strings.TrimSpace(input.Method))
}
