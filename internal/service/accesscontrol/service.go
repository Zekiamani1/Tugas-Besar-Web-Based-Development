package accesscontrol

import (
	"context"

	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Register(ctx context.Context, username, password string) error {
	return nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	return "", nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return nil
}

func (s *Service) ValidateSession(ctx context.Context, sessionID string) error {
	return nil
}

func (s *Service) CreateGroup(ctx context.Context, groupName string) error {
	return nil
}

func (s *Service) AssignUserToGroup(ctx context.Context, userID, groupID int) error {
	return nil
}

func (s *Service) SetGroupDBAccess(ctx context.Context, groupID, targetDBID int, accessLevel string) error {
	return nil
}

func (s *Service) EvaluateDBAccess(ctx context.Context, userID, targetDBID int) error {
	return nil
}
