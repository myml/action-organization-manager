package main

import (
	"fmt"
	"io/ioutil"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Organization string    `yaml:"organization"`
	Settings     []Setting `yaml:"settings"`
}

type Setting struct {
	Repositories []string            `yaml:"repositories"`
	Features     Features            `yaml:"features"`
	Branches     map[string]Branches `yaml:"branches"`
}

type Features struct {
	Issues           FeatureOption `yaml:"issues"`
	Wiki             FeatureOption `yaml:"wiki"`
	Projects         FeatureOption `yaml:"projects"`
	AllowMergeCommit FeatureOption `yaml:"allow_merge_commit"`
	AllowRebaseMerge FeatureOption `yaml:"allow_rebase_merge"`
	AllowSquashMerge FeatureOption `yaml:"allow_squash_merge"`
}

type FeatureOption struct {
	Enable *bool `yaml:"enable"`
}

type Branches struct {
	DismissStaleReviews          *bool                `yaml:"dismiss_stale_reviews"`
	EnforceAdmins                *bool                `yaml:"enforce_admins"`
	RequiredApprovingReviewCount *int32               `yaml:"required_approving_review_count"`
	RequiredStatusChecks         RequiredStatusChecks `yaml:"required_status_checks"`
}
type RequiredStatusChecks struct {
	Strict *bool `yaml:"strict"`
	// RequireReview *bool    `yaml:"require_review"`
	Content []string `yaml:"content"`
}

func ParseConfigFile(filename string) (*Config, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	return &config, nil
}
