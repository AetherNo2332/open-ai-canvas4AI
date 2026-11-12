package app

import (
	"errors"
	"gorm.io/gorm"
	cp "yingce/backend/internal/creationplatform"
)

func EcommerceAgentNotFound(err error) bool        { return errors.Is(err, gorm.ErrRecordNotFound) }
func CreationPlatformFor(svc *Service) cp.Platform { return svc.CreationPlatform() }
