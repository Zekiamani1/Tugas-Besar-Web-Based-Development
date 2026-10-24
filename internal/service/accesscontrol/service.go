package accesscontrol

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wbd/internal/models"
)

type SessionConfig struct {
	AbsoluteTTL time.Duration
	IdleTimeout time.Duration
}

type Service struct {
	db            *gorm.DB
	sessionConfig SessionConfig
}

const (
	AccessLevelViewOnly = "VIEW_ONLY"
	AccessLevelViewEdit = "VIEW_EDIT"
)

func NewService(db *gorm.DB, sessionConfig SessionConfig) *Service {
	db.AutoMigrate(&models.User{}, &models.Session{}, &models.Group{}, &models.UserGroup{}, &models.GroupDBAccess{})
	seedAdmin(db)
	return &Service{db: db, sessionConfig: sessionConfig}
}

func seedAdmin(db *gorm.DB) {
	username := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	if username == "" {
		username = strings.TrimSpace(os.Getenv("INITIAL_ADMIN_USERNAME"))
	}
	password := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))
	if password == "" {
		password = strings.TrimSpace(os.Getenv("INITIAL_ADMIN_PASSWORD"))
	}
	if username == "" || password == "" {
		return
	}

	var existing models.User
	if err := db.Where("username = ?", username).First(&existing).Error; err == nil {
		if !existing.IsAdmin {
			db.Model(&models.User{}).Where("id = ?", existing.ID).Update("is_admin", true)
		}
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return
	}

	admin := models.User{
		Username:     username,
		PasswordHash: string(hash),
		IsAdmin:      true,
	}
	db.Clauses(clause.OnConflict{DoNothing: true}).Create(&admin)
}

// fungsi tambahan buat cek apakah admint
func (s *Service) IsAdmin(ctx context.Context, userID int) (bool, error) {
    if userID <= 0 {
        return false, errors.New("user id must be positive")
    }

    var user models.User

    tx := s.db.WithContext(ctx).
        Select("id", "is_admin").
        First(&user, userID)

    if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
        return false, nil
    }

    if tx.Error != nil {
        return false, fmt.Errorf("check platform admin: %w", tx.Error)
    }

    return user.IsAdmin, nil
}

func (s *Service) Register(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("Username tidak boleh kosong")
	}
	if password == "" {
		return errors.New("Password tidak boleh kosong")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	user := models.User{
		Username:     username,
		PasswordHash: string(passwordHash),
	}
	tx := s.db.WithContext(ctx).Create(&user)

	if tx.Error != nil {
		return fmt.Errorf("gagal menyimpan user ke database: %w", tx.Error)
	}

	return nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", errors.New("Username tidak boleh kosong")
	}
	if password == "" {
		return "", errors.New("Password tidak boleh kosong")
	}

	var user models.User
	tx := s.db.WithContext(ctx).
		Where("username = ?", username).
		First(&user)

	if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
		return "", errors.New("username atau password salah")
	}
	if tx.Error != nil {
		return "", fmt.Errorf("find user: %w", tx.Error)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", errors.New("username atau password salah")
	}

	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	sessionID := base64.RawURLEncoding.EncodeToString(randomBytes)

	now := time.Now().UTC()
	session := models.Session{
		UserID:     user.ID,
		SessionID:  sessionID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.sessionConfig.AbsoluteTTL),
	}

	tx = s.db.WithContext(ctx).Create(&session)
	if tx.Error != nil {
		return "", fmt.Errorf("create session: %w", tx.Error)
	}

	return sessionID, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}

	tx := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Delete(&models.Session{})
	if tx.Error != nil {
		return fmt.Errorf("revoke session: %w", tx.Error)
	}
	return nil
}

var ErrInvalidSession = errors.New("invalid or expired session")

func (s *Service) ValidateSession(ctx context.Context, sessionID string) (int, error) { //nambah nge return userid supaya tau session yang di cek itu punya siapa
	if sessionID == "" {
		return 0, ErrInvalidSession
	}

	var session models.Session
	tx := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		First(&session)
	if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
		return 0, ErrInvalidSession
	}
	if tx.Error != nil {
		return 0, fmt.Errorf("find session: %w", tx.Error)
	}
	var user models.User

	if err := s.db.WithContext(ctx).
		Select("id").
		First(&user, session.UserID).Error; err != nil {

		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrInvalidSession
		}

		return 0, fmt.Errorf("find session owner: %w", err)
	}

	now := time.Now().UTC()

	idleExpired := now.Sub(session.LastSeenAt) >= s.sessionConfig.IdleTimeout

	absoluteExpired := !now.Before(session.ExpiresAt)

	if idleExpired || absoluteExpired {
		if err := s.db.WithContext(ctx).Delete(&session).Error; err != nil {
			return 0, fmt.Errorf("delete expired session: %w", err)
		}
		return 0, ErrInvalidSession
	}
	update := s.db.WithContext(ctx).
		Model(&models.Session{}).
		Where("id = ?", session.ID).
		Update("last_seen_at", now)

	if update.Error != nil {
		return 0, fmt.Errorf("Update session activity: %w", update.Error)
	}
	if update.RowsAffected == 0 {
		return 0, ErrInvalidSession
	}
	return session.UserID, nil
}

func (s *Service) CreateGroup(ctx context.Context, groupName string) error {
	groupName = strings.TrimSpace(groupName)

	if groupName == "" {
		return errors.New("group name cannot be empty")
	}

	group := models.Group{Name: groupName}

	tx := s.db.WithContext(ctx).Create(&group)

	if tx.Error != nil {
		return fmt.Errorf("create group: %w", tx.Error)
	}
	return nil
}

func (s *Service) AssignUserToGroup(ctx context.Context, userID, groupID int) error {
	if userID <= 0 {
		return errors.New("user id must be positive")
	}
	if groupID <= 0 {
		return errors.New("group id must be positive")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {

		var user models.User

		if err := tx.First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("User %d not found", userID)
			}
			return fmt.Errorf("find user: %w", err)

		}

		var group models.Group

		if err := tx.First(&group, groupID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("group %d not found", groupID)
			}
			return fmt.Errorf("find group: %w", err)
		}

		membership := models.UserGroup{
			UserID:  userID,
			GroupID: groupID,
		}

		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&membership).Error; err != nil {
			return fmt.Errorf("assign user to group: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("assign user to group transaction: %w", err)
	}

	return nil
}

func (s *Service) SetGroupDBAccess(ctx context.Context, groupID, targetDBID int, accessLevel string) error {
	if groupID <= 0 {
		return errors.New("group id must be positive")
	}
	if targetDBID <= 0 {
		return errors.New("target database id must be positive")
	}

	accessLevel = strings.ToUpper(strings.TrimSpace(accessLevel))

	switch accessLevel {
	case AccessLevelViewEdit, AccessLevelViewOnly:
	default:
		return errors.New("Invalid level acces")
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var group models.Group

		if err := tx.First(&group, groupID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("group %d not found", groupID)
			}
			return fmt.Errorf("find group: %w", err)
		}

		var targetDB models.TargetDBInfo

		if err := tx.First(&targetDB, targetDBID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("targetDB %d not found", targetDBID)
			}
			return fmt.Errorf("find targetDB: %w", err)
		}

		grantacc := models.GroupDBAccess{
			GroupID:     groupID,
			TargetDBID:  targetDBID,
			AccessLevel: accessLevel,
		}

		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "group_id"},
				{Name: "target_db_id"},
			},

			DoUpdates: clause.AssignmentColumns([]string{"access_level"}),
		}).Create(&grantacc).Error; err != nil {
			return fmt.Errorf("save group db acces %w", err)
		}
		return nil

	})
	if err != nil {
		return fmt.Errorf("set group database access: %w", err)
	}
	return nil
}

var ErrDBAccessDenied = errors.New("database access denied")

func (s *Service) EvaluateDBAccess(ctx context.Context, userID, targetDBID int, requiredLevel string) error {
	if userID <= 0 {
		return errors.New("user id must be positive")
	}
	if targetDBID <= 0 {
		return errors.New("target database id must be positive")
	}
	requiredLevel = strings.ToUpper(strings.TrimSpace(requiredLevel))

	switch requiredLevel {
	case AccessLevelViewEdit, AccessLevelViewOnly:
	default:
		return errors.New("Invalid level acces")
	}

	var accessLevels []string
	tx := s.db.WithContext(ctx).
		Table("user_groups AS ug").
		Select("gda.access_level").
		Joins("JOIN group_db_accesses AS gda ON gda.group_id = ug.group_id").
		Where("ug.user_id = ? AND gda.target_db_id = ?", userID, targetDBID).
		Scan(&accessLevels)
	if tx.Error != nil {
		return fmt.Errorf("database error %w", tx.Error)
	}

	hasViewOnly := false

	for _, level := range accessLevels {

		switch level {

		case AccessLevelViewEdit:
			return nil

		case AccessLevelViewOnly:
			hasViewOnly = true

		default:
			return fmt.Errorf("unkown %q", level)

		}
	}

	if requiredLevel == AccessLevelViewOnly && hasViewOnly {
		return nil
	}
	return ErrDBAccessDenied
}
