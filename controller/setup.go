package controller

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Setup struct {
	Status       bool   `json:"status"`
	RootInit     bool   `json:"root_init"`
	DatabaseType string `json:"database_type"`
}

type SetupRequest struct {
	Username           string `json:"username"`
	Password           string `json:"password"`
	ConfirmPassword    string `json:"confirmPassword"`
	SelfUseModeEnabled bool   `json:"SelfUseModeEnabled"`
	DemoSiteEnabled    bool   `json:"DemoSiteEnabled"`
}

var setupCompletionMu sync.Mutex

func getExistingRootUser() (*model.User, error) {
	if model.DB == nil {
		return nil, nil
	}
	var root model.User
	err := model.DB.Where("role = ?", common.RoleRootUser).Order("id").First(&root).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &root, nil
}

func getPersistedSetupOptions() (selfUse bool, demoSite bool, ok bool) {
	if model.DB == nil {
		return false, false, false
	}
	var opts []model.Option
	if err := model.DB.Where("key IN ?", []string{"SelfUseModeEnabled", "DemoSiteEnabled"}).Find(&opts).Error; err != nil {
		return false, false, false
	}
	var hasSelfUse, hasDemo bool
	for _, opt := range opts {
		switch opt.Key {
		case "SelfUseModeEnabled":
			hasSelfUse = true
			selfUse = opt.Value == "true"
		case "DemoSiteEnabled":
			hasDemo = true
			demoSite = opt.Value == "true"
		}
	}
	return selfUse, demoSite, hasSelfUse && hasDemo
}

func applySetupMemoryOptions(selfUse, demoSite bool) {
	operation_setting.SetSelfUseModeEnabled(selfUse)
	operation_setting.SetDemoSiteEnabled(demoSite)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap != nil {
		common.OptionMap["SelfUseModeEnabled"] = boolToString(selfUse)
		common.OptionMap["DemoSiteEnabled"] = boolToString(demoSite)
	}
	common.OptionMapRWMutex.Unlock()
}

// syncSetupStateFromDB reconciles in-memory setup state with persisted DB state.
// Setup is only considered complete in DB when a live Root user exists, both
// operation mode options have been persisted, and the setup completion row exists.
func syncSetupStateFromDB() bool {
	if model.DB == nil {
		return constant.Setup
	}
	rootExists := model.RootUserExists()
	setupRow := model.GetSetup()
	selfUse, demoSite, optionsOK := getPersistedSetupOptions()
	if rootExists && setupRow != nil && optionsOK {
		applySetupMemoryOptions(selfUse, demoSite)
		constant.Setup = true
		return true
	}
	if rootExists || setupRow != nil {
		// Half-initialized DB state: never leave in-memory Setup=true blocking recovery.
		constant.Setup = false
		return false
	}
	return constant.Setup
}

func GetSetup(c *gin.Context) {
	completed := syncSetupStateFromDB()
	setup := Setup{
		Status: completed,
	}
	if completed {
		c.JSON(200, gin.H{
			"success": true,
			"data":    setup,
		})
		return
	}
	setup.RootInit = model.RootUserExists()
	setup.DatabaseType = string(common.MainDatabaseType())
	c.JSON(200, gin.H{
		"success": true,
		"data":    setup,
	})
}

func PostSetup(c *gin.Context) {
	// The in-memory flag is per process. Another instance, or a restart that
	// already completed all setup records, must not accept another anonymous write.
	if syncSetupStateFromDB() {
		common.ApiErrorT(c, "The system is already initialized")
		return
	}

	var req SetupRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	req.Username = strings.TrimSpace(req.Username)

	existingRoot, err := getExistingRootUser()
	if err != nil {
		common.SysError(common.LogText("setup check root user failed: %v", err))
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}

	if existingRoot != nil {
		// Root was already committed in an earlier attempt whose trailing option
		// or setup-row write failed (or by a concurrent winner). Only a retry
		// presenting the established Root credentials may finish the tail;
		// never reset Root's password or allow anonymous takeover.
		if req.Username == "" || req.Username != existingRoot.Username ||
			req.Password == "" || req.Password != req.ConfirmPassword ||
			!common.ValidatePasswordAndHash(req.Password, existingRoot.Password) {
			common.ApiErrorT(c, "The system is already initialized")
			return
		}
	} else {
		if req.Username == "" {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		// Same username rule as registration: at most 20 characters, and no @.
		if len(req.Username) > 20 {
			common.ApiErrorT(c, "Username must be {{max}} characters or fewer", map[string]any{"max": 20})
			return
		}
		if err := common.Validate.StructPartial(&model.User{Username: req.Username}, "Username"); err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		// Validate password
		if req.Password != req.ConfirmPassword {
			common.ApiErrorT(c, "Passwords do not match")
			return
		}

		if err := common.ValidateNewAccountPassword(req.Password); err != nil {
			common.ApiError(c, err)
			return
		}

		// Create root user
		hashedPassword, err := common.HashAccountPassword(req.Password)
		if err != nil {
			common.SysError(common.LogText("setup hash password failed: %v", err))
			common.ApiErrorT(c, "Database error, please contact the administrator")
			return
		}
		rootUser := model.User{
			Username:    req.Username,
			Password:    hashedPassword,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
			AccessToken: nil,
			Quota:       100000000,
		}
		err = model.CreateRootUserIfAbsent(&rootUser)
		if err != nil {
			common.SysError(common.LogText("setup create root user failed: %v", err))
			common.ApiErrorT(c, "Database error, please contact the administrator")
			return
		}
		currentRoot, err := getExistingRootUser()
		if err != nil || currentRoot == nil {
			if err != nil {
				common.SysError(common.LogText("setup verify root user failed: %v", err))
			}
			common.ApiErrorT(c, "Database error, please contact the administrator")
			return
		}
		if currentRoot.Username != req.Username || !common.ValidatePasswordAndHash(req.Password, currentRoot.Password) {
			syncSetupStateFromDB()
			common.ApiErrorT(c, "The system is already initialized")
			return
		}
	}

	setupCompletionMu.Lock()
	defer setupCompletionMu.Unlock()

	if syncSetupStateFromDB() {
		common.ApiErrorT(c, "The system is already initialized")
		return
	}

	prevSelfUse := operation_setting.GetSelfUseModeEnabled()
	prevDemoSite := operation_setting.GetDemoSiteEnabled()
	common.OptionMapRWMutex.RLock()
	prevSelfOpt, hadSelfOpt := common.OptionMap["SelfUseModeEnabled"]
	prevDemoOpt, hadDemoOpt := common.OptionMap["DemoSiteEnabled"]
	common.OptionMapRWMutex.RUnlock()

	restoreMemory := func() {
		operation_setting.SetSelfUseModeEnabled(prevSelfUse)
		operation_setting.SetDemoSiteEnabled(prevDemoSite)
		common.OptionMapRWMutex.Lock()
		if common.OptionMap != nil {
			if hadSelfOpt {
				common.OptionMap["SelfUseModeEnabled"] = prevSelfOpt
			} else {
				delete(common.OptionMap, "SelfUseModeEnabled")
			}
			if hadDemoOpt {
				common.OptionMap["DemoSiteEnabled"] = prevDemoOpt
			} else {
				delete(common.OptionMap, "DemoSiteEnabled")
			}
		}
		common.OptionMapRWMutex.Unlock()
		constant.Setup = false
	}

	// Save operation modes to database for persistence
	err = model.UpdateOption("SelfUseModeEnabled", boolToString(req.SelfUseModeEnabled))
	if err != nil {
		restoreMemory()
		common.SysError(common.LogText("setup save SelfUseModeEnabled failed: %v", err))
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}

	err = model.UpdateOption("DemoSiteEnabled", boolToString(req.DemoSiteEnabled))
	if err != nil {
		restoreMemory()
		common.SysError(common.LogText("setup save DemoSiteEnabled failed: %v", err))
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}

	if model.GetSetup() == nil {
		setup := model.Setup{
			Version:       common.Version,
			InitializedAt: time.Now().Unix(),
		}
		err = model.DB.Create(&setup).Error
		if err != nil {
			restoreMemory()
			common.SysError(common.LogText("setup create record failed: %v", err))
			common.ApiErrorT(c, "Database error, please contact the administrator")
			return
		}
	}

	// Only mark in-memory setup complete after root, options, and setup row are all persisted.
	applySetupMemoryOptions(req.SelfUseModeEnabled, req.DemoSiteEnabled)
	constant.Setup = true

	common.ApiSuccessT(c, "System initialized successfully", nil)
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
