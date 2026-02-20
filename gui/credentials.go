package main

import (
	"encoding/json"
	"errors"

	"github.com/zalando/go-keyring"
)

const keychainService = "com.tigrisdata.tigrisfs"

type profileCredentials struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func credentialKey(profileName string) string {
	return "profile/" + normalizeProfileName(profileName)
}

func attachStoredCredentials(profile *ConnectionProfile) {
	if profile == nil {
		return
	}
	accessKey, secretKey, err := loadProfileCredentials(profile.Name)
	if err != nil {
		return
	}
	if profile.AccessKey == "" {
		profile.AccessKey = accessKey
	}
	if profile.SecretKey == "" {
		profile.SecretKey = secretKey
	}
}

func loadProfileCredentials(profileName string) (string, string, error) {
	raw, err := keyring.Get(keychainService, credentialKey(profileName))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", "", nil
		}
		return "", "", err
	}

	var creds profileCredentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return "", "", err
	}
	return creds.AccessKey, creds.SecretKey, nil
}

func saveProfileCredentials(profileName, accessKey, secretKey string) error {
	if accessKey == "" && secretKey == "" {
		return deleteProfileCredentials(profileName)
	}

	payload, err := json.Marshal(profileCredentials{
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err != nil {
		return err
	}
	return keyring.Set(keychainService, credentialKey(profileName), string(payload))
}

func deleteProfileCredentials(profileName string) error {
	err := keyring.Delete(keychainService, credentialKey(profileName))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
