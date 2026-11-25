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
	AbsoluteTTL      time.Duration
	IdleTimeout      time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
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
	db.AutoMigrate(&models.User{}, &models.Session{}, &models.SessionAlias{}, &models.Group{}, &models.UserGroup{}, &models.GroupDBAccess{})
	seedAdmin(db, sessionConfig)
	return &Service{db: db, sessionConfig: sessionConfig}
}

func seedAdmin(db *gorm.DB, sessionConfig SessionConfig) {
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
			_ = db.Transaction(func(tx *gorm.DB) error {
				result := tx.Model(&models.User{}).
					Where("id = ? AND is_admin = ?", existing.ID, false).
					Update("is_admin", true)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected > 0 {
					return rotateUserSessions(tx, []int{existing.ID}, time.Now().UTC(), sessionConfig.GracePeriod)
				}
				return nil
			})
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

func (s *Service) ListUsers(ctx context.Context) ([]models.UserSummary, error) {
	var users []models.UserSummary
	if err := s.db.WithContext(ctx).
		Model(&models.User{}).
		Select("id", "username", "is_admin").
		Order("id ASC").
		Find(&users).Error; err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (s *Service) ListGroups(ctx context.Context) ([]models.Group, error) {
	var groups []models.Group
	if err := s.db.WithContext(ctx).
		Model(&models.Group{}).
		Select("id", "name").
		Order("id ASC").
		Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return groups, nil
}

func (s *Service) ListGroupMembers(ctx context.Context) ([]models.GroupMemberSummary, error) {
	var members []models.GroupMemberSummary
	err := s.db.WithContext(ctx).
		Table("user_groups").
		Select("user_groups.group_id, users.id AS user_id, users.username").
		Joins("JOIN users ON users.id = user_groups.user_id").
		Order("user_groups.group_id ASC, users.id ASC").
		Scan(&members).Error
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	return members, nil
}

func (s *Service) ListGroupDBAccess(ctx context.Context) ([]models.GroupDBAccessSummary, error) {
	var accesses []models.GroupDBAccessSummary
	err := s.db.WithContext(ctx).
		Table("group_db_accesses").
		Select("group_db_accesses.group_id, group_db_accesses.target_db_id, target_db_infos.name AS database_name, group_db_accesses.access_level").
		Joins("JOIN target_db_infos ON target_db_infos.id = group_db_accesses.target_db_id").
		Order("group_db_accesses.group_id ASC, group_db_accesses.target_db_id ASC").
		Scan(&accesses).Error
	if err != nil {
		return nil, fmt.Errorf("list group database access: %w", err)
	}
	return accesses, nil
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

	sessionID, err := newSessionID()
	if err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}

	now := time.Now().UTC()
	session := models.Session{
		UserID:        user.ID,
		SessionID:     sessionID,
		CreatedAt:     now,
		LastSeenAt:    now,
		LastRotatedAt: now,
		ExpiresAt:     now.Add(s.sessionConfig.AbsoluteTTL),
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
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session models.Session
		err := tx.Where("session_id = ?", sessionID).First(&session).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var alias models.SessionAlias
			err = tx.Where("session_id = ? AND expires_at > ?", sessionID, time.Now().UTC()).First(&alias).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("find session alias for logout: %w", err)
			}
			err = tx.First(&session, alias.SessionRecordID).Error
		}
		if err != nil {
			return fmt.Errorf("find session for logout: %w", err)
		}
		if err := tx.Where("session_record_id = ?", session.ID).Delete(&models.SessionAlias{}).Error; err != nil {
			return fmt.Errorf("delete session aliases: %w", err)
		}
		if err := tx.Delete(&session).Error; err != nil {
			return fmt.Errorf("revoke session: %w", err)
		}
		return nil
	})
}

var ErrInvalidSession = errors.New("invalid or expired session")

func (s *Service) ValidateSession(ctx context.Context, sessionID string) (int, error) {
	validation, err := s.validateSession(ctx, sessionID, false)
	if err != nil {
		return 0, err
	}
	return validation.UserID, nil
}

func (s *Service) ValidateSessionWithRotation(ctx context.Context, sessionID string) (models.SessionValidation, error) {
	return s.validateSession(ctx, sessionID, true)
}

func (s *Service) validateSession(ctx context.Context, sessionID string, rotate bool) (models.SessionValidation, error) {
	if sessionID == "" {
		return models.SessionValidation{}, ErrInvalidSession
	}

	now := time.Now().UTC()
	var validation models.SessionValidation
	invalidated := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session models.Session
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("session_id = ?", sessionID).
			First(&session).Error
		isCurrentID := err == nil
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var alias models.SessionAlias
			err = tx.Where("session_id = ? AND expires_at > ?", sessionID, now).First(&alias).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidSession
			}
			if err != nil {
				return fmt.Errorf("find session alias: %w", err)
			}
			err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, alias.SessionRecordID).Error
			isCurrentID = false
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidSession
		}
		if err != nil {
			return fmt.Errorf("find session: %w", err)
		}

		var user models.User
		if err := tx.Select("id").First(&user, session.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidSession
			}
			return fmt.Errorf("find session owner: %w", err)
		}

		if now.Sub(session.LastSeenAt) >= s.sessionConfig.IdleTimeout || !now.Before(session.ExpiresAt) {
			if err := tx.Where("session_record_id = ?", session.ID).Delete(&models.SessionAlias{}).Error; err != nil {
				return fmt.Errorf("delete expired session aliases: %w", err)
			}
			if err := tx.Delete(&session).Error; err != nil {
				return fmt.Errorf("delete expired session: %w", err)
			}
			invalidated = true
			return nil
		}

		updates := map[string]any{"last_seen_at": now}

		if rotate && isCurrentID && s.sessionConfig.RotationInterval > 0 && now.Sub(session.LastRotatedAt) >= s.sessionConfig.RotationInterval {
			newID, err := newSessionID()
			if err != nil {
				return fmt.Errorf("generate rotated session ID: %w", err)
			}
			graceExpiresAt := now.Add(s.sessionConfig.GracePeriod)
			if graceExpiresAt.After(session.ExpiresAt) {
				graceExpiresAt = session.ExpiresAt
			}
			if now.Before(graceExpiresAt) {
				alias := models.SessionAlias{SessionID: session.SessionID, SessionRecordID: session.ID, ExpiresAt: graceExpiresAt}
				if err := tx.Create(&alias).Error; err != nil {
					return fmt.Errorf("save previous session identifier: %w", err)
				}
			}
			updates["session_id"] = newID
			updates["last_rotated_at"] = now
			session.SessionID = newID
		}

		update := tx.Model(&models.Session{}).Where("id = ?", session.ID).Updates(updates)
		if update.Error != nil {
			return fmt.Errorf("update session activity: %w", update.Error)
		}
		if update.RowsAffected == 0 {
			return ErrInvalidSession
		}
		validation = models.SessionValidation{
			UserID:           session.UserID,
			CurrentSessionID: session.SessionID,
			UsedGracePeriod:  !isCurrentID,
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrInvalidSession) {
			return models.SessionValidation{}, ErrInvalidSession
		}
		return models.SessionValidation{}, err
	}
	if invalidated {
		return models.SessionValidation{}, ErrInvalidSession
	}
	return validation, nil
}

func newSessionID() (string, error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func rotateUserSessions(tx *gorm.DB, userIDs []int, now time.Time, gracePeriod time.Duration) error {
	if len(userIDs) == 0 {
		return nil
	}
	var sessions []models.Session
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id IN ?", userIDs).Find(&sessions).Error; err != nil {
		return fmt.Errorf("find affected sessions: %w", err)
	}
	for _, session := range sessions {
		newID, err := newSessionID()
		if err != nil {
			return fmt.Errorf("generate rotated session ID: %w", err)
		}
		graceExpiresAt := now.Add(gracePeriod)
		if graceExpiresAt.After(session.ExpiresAt) {
			graceExpiresAt = session.ExpiresAt
		}
		if now.Before(graceExpiresAt) {
			alias := models.SessionAlias{SessionID: session.SessionID, SessionRecordID: session.ID, ExpiresAt: graceExpiresAt}
			if err := tx.Create(&alias).Error; err != nil {
				return fmt.Errorf("save previous session identifier: %w", err)
			}
		}
		if err := tx.Model(&models.Session{}).Where("id = ?", session.ID).Updates(map[string]any{
			"session_id":      newID,
			"last_rotated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("rotate session for user %d: %w", session.UserID, err)
		}
	}
	return nil
}

func revokeUserSessions(tx *gorm.DB, userIDs []int) error {
	if len(userIDs) == 0 {
		return nil
	}
	var sessionIDs []int
	if err := tx.Model(&models.Session{}).Where("user_id IN ?", userIDs).Pluck("id", &sessionIDs).Error; err != nil {
		return fmt.Errorf("find sessions to revoke: %w", err)
	}
	if len(sessionIDs) == 0 {
		return nil
	}
	if err := tx.Where("session_record_id IN ?", sessionIDs).Delete(&models.SessionAlias{}).Error; err != nil {
		return fmt.Errorf("delete revoked session aliases: %w", err)
	}
	if err := tx.Where("id IN ?", sessionIDs).Delete(&models.Session{}).Error; err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
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

		membershipResult := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&membership)
		if membershipResult.Error != nil {
			return fmt.Errorf("assign user to group: %w", membershipResult.Error)
		}
		if membershipResult.RowsAffected > 0 {
			var permissionCount int64
			if err := tx.Model(&models.GroupDBAccess{}).Where("group_id = ?", groupID).Count(&permissionCount).Error; err != nil {
				return fmt.Errorf("count group database permissions: %w", err)
			}
			if permissionCount > 0 {
				if err := rotateUserSessions(tx, []int{userID}, time.Now().UTC(), s.sessionConfig.GracePeriod); err != nil {
					return err
				}
			}
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

		var existingAccess models.GroupDBAccess
		accessErr := tx.Where("group_id = ? AND target_db_id = ?", groupID, targetDBID).
			First(&existingAccess).Error
		if accessErr != nil && !errors.Is(accessErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("find existing group database access: %w", accessErr)
		}
		privilegeIncreased := errors.Is(accessErr, gorm.ErrRecordNotFound) ||
			(existingAccess.AccessLevel == AccessLevelViewOnly && accessLevel == AccessLevelViewEdit)
		privilegeDecreased := accessErr == nil &&
			existingAccess.AccessLevel == AccessLevelViewEdit && accessLevel == AccessLevelViewOnly

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
		if privilegeIncreased || privilegeDecreased {
			var userIDs []int
			if err := tx.Model(&models.UserGroup{}).Where("group_id = ?", groupID).Pluck("user_id", &userIDs).Error; err != nil {
				return fmt.Errorf("find group members affected by permission change: %w", err)
			}
			if privilegeIncreased {
				if err := rotateUserSessions(tx, userIDs, time.Now().UTC(), s.sessionConfig.GracePeriod); err != nil {
					return err
				}
			} else if err := revokeUserSessions(tx, userIDs); err != nil {
				return err
			}
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
