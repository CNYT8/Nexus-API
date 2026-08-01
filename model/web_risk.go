package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	WebRiskWindowSeconds   int64 = 5 * 60
	WebRiskDistinctIPLimit       = 3
)

type WebRiskState struct {
	UserId       int    `json:"user_id" gorm:"primaryKey"`
	RecentIPs    string `json:"-" gorm:"type:text;not null"`
	Challenged   bool   `json:"challenged" gorm:"not null;default:false;index"`
	ChallengedAt int64  `json:"challenged_at" gorm:"not null;default:0"`
	UpdatedAt    int64  `json:"updated_at" gorm:"not null;default:0;index"`
}

func (WebRiskState) TableName() string {
	return "nexus_web_risk_states"
}

type WebRiskStatus struct {
	Challenged  bool `json:"challenged"`
	DistinctIPs int  `json:"distinct_ips"`
}

type webRiskIPEntry struct {
	Hash       string `json:"hash"`
	LastSeenAt int64  `json:"last_seen_at"`
}

func webRiskIPHash(ip string) (string, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", errors.New("web risk IP is empty")
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	}
	secret := strings.TrimSpace(common.GetWebRiskFingerprintSecret())
	if secret == "" && DB != nil {
		if err := initializeWebRiskFingerprintSecret(); err != nil {
			return "", err
		}
		secret = strings.TrimSpace(common.GetWebRiskFingerprintSecret())
	}
	if secret == "" {
		return "", errors.New("web risk hash secret is not initialized")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("nexus-web-risk:" + ip))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func WebRiskIPFingerprint(ip string) (string, error) {
	return webRiskIPHash(ip)
}

func decodeWebRiskIPs(value string) ([]webRiskIPEntry, error) {
	if strings.TrimSpace(value) == "" {
		return []webRiskIPEntry{}, nil
	}
	var entries []webRiskIPEntry
	if err := json.Unmarshal([]byte(value), &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func ObserveWebRiskIP(userId int, ip string) (WebRiskStatus, error) {
	return observeWebRiskIPAt(userId, ip, time.Now().Unix())
}

func observeWebRiskIPAt(userId int, ip string, now int64) (status WebRiskStatus, err error) {
	if userId <= 0 || now <= 0 {
		return status, errors.New("invalid web risk observation")
	}
	ipHash, err := webRiskIPHash(ip)
	if err != nil {
		return status, err
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		initial := WebRiskState{UserId: userId, RecentIPs: "[]", UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&initial).Error; err != nil {
			return err
		}

		var state WebRiskState
		if err := lockForUpdate(tx).Where("user_id = ?", userId).First(&state).Error; err != nil {
			return err
		}
		entries, err := decodeWebRiskIPs(state.RecentIPs)
		if err != nil {
			return err
		}
		if state.Challenged {
			status = WebRiskStatus{Challenged: true, DistinctIPs: len(entries)}
			return nil
		}

		cutoff := now - WebRiskWindowSeconds
		active := make([]webRiskIPEntry, 0, len(entries)+1)
		found := false
		for _, entry := range entries {
			if entry.Hash == "" || entry.LastSeenAt <= cutoff {
				continue
			}
			if entry.Hash == ipHash {
				entry.LastSeenAt = now
				found = true
			}
			active = append(active, entry)
		}
		if !found {
			active = append(active, webRiskIPEntry{Hash: ipHash, LastSeenAt: now})
		}
		state.Challenged = len(active) >= WebRiskDistinctIPLimit
		if state.Challenged {
			state.ChallengedAt = now
		}
		state.UpdatedAt = now
		encoded, err := json.Marshal(active)
		if err != nil {
			return err
		}
		state.RecentIPs = string(encoded)
		if err := tx.Save(&state).Error; err != nil {
			return err
		}
		status = WebRiskStatus{Challenged: state.Challenged, DistinctIPs: len(active)}
		return nil
	})
	return status, err
}

func GetWebRiskStatus(userId int) (WebRiskStatus, error) {
	if userId <= 0 {
		return WebRiskStatus{}, errors.New("invalid web risk user")
	}
	var state WebRiskState
	err := DB.Where("user_id = ?", userId).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return WebRiskStatus{}, nil
	}
	if err != nil {
		return WebRiskStatus{}, err
	}
	entries, err := decodeWebRiskIPs(state.RecentIPs)
	if err != nil {
		return WebRiskStatus{}, err
	}
	return WebRiskStatus{Challenged: state.Challenged, DistinctIPs: len(entries)}, nil
}

func ResetWebRisk(userId int) error {
	if userId <= 0 {
		return errors.New("invalid web risk user")
	}
	return DB.Model(&WebRiskState{}).
		Where("user_id = ?", userId).
		Updates(map[string]interface{}{
			"recent_ips":    "[]",
			"challenged":    false,
			"challenged_at": 0,
			"updated_at":    time.Now().Unix(),
		}).Error
}
