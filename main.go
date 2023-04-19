package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/github"
	"github.com/shurcooL/githubv4"
	"github.com/shurcooL/graphql"
)

func main() {
	var configFile string
	var appID, installationID int64
	flag.StringVar(&configFile, "f", "config.yaml", "config file")
	flag.Int64Var(&appID, "app_id", 0, "*github app id")
	flag.Int64Var(&installationID, "installation_id", 0, "*github installation id")
	flag.Parse()
	if appID == 0 || installationID == 0 {
		flag.PrintDefaults()
		return
	}

	config, err := ParseConfigFile(configFile)
	if err != nil {
		log.Fatal(err)
	}
	privateKey := []byte(os.Getenv("PRIVATE_KEY"))
	itr, err := ghinstallation.New(http.DefaultTransport, appID, installationID, []byte(privateKey))
	if err != nil {
		log.Fatal(err)
	}
	client := github.NewClient(&http.Client{Transport: itr})
	clientv4 := githubv4.NewClient(&http.Client{Transport: itr})
	err = run(context.Background(), client, clientv4, config)
	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, client *github.Client, clientv4 *githubv4.Client, config *Config) error {
	opt := github.RepositoryListByOrgOptions{
		Type: "public",
	}
	for {
		ownerName := config.Organization
		repos, pageResp, err := client.Repositories.ListByOrg(context.Background(), ownerName, &opt)
		if err != nil {
			log.Fatal(err)
		}
		time.Sleep(time.Second)
		limitWait(&pageResp.Rate)
		for _, repo := range repos {
			repoName := repo.GetName()
			log.Println("find repo:", *repo.FullName)
			for _, setting := range config.Settings {
				for _, repoRegexp := range setting.Repositories {
					match, err := regexp.MatchString(repoRegexp, repoName)
					if err != nil {
						return fmt.Errorf("%s match %s failed: %w", repoRegexp, repoName, err)
					}
					if !match {
						continue
					}
					log.Println("match to setting:", repoRegexp)

					resp, err := featuresSync(ctx, client, repo.GetFullName(), setting.Features)
					if err != nil {
						return err
					}
					time.Sleep(time.Second)
					limitWait(&resp.Rate)

					for branchRule := range setting.Branches {
						log.Println("\t", branchRule)
						err := branchesSync(ctx, client, clientv4, ownerName, repoName, branchRule, setting.Branches[branchRule])
						if err != nil {
							return err
						}
						time.Sleep(time.Second)
					}
				}
			}
		}
		if pageResp.NextPage == 0 {
			break
		}
		opt.Page = pageResp.NextPage
	}
	return nil
}

func featuresSync(ctx context.Context, client *github.Client, repo string, features Features) (*github.Response, error) {
	var r github.Repository
	if features.Issues.Enable != nil {
		r.HasIssues = features.Issues.Enable
	}
	if features.Projects.Enable != nil {
		r.HasProjects = features.Projects.Enable
	}
	if features.Wiki.Enable != nil {
		r.HasWiki = features.Wiki.Enable
	}
	r.AllowMergeCommit = features.AllowMergeCommit.Enable
	r.AllowRebaseMerge = features.AllowRebaseMerge.Enable
	r.AllowSquashMerge = features.AllowSquashMerge.Enable
	owner, repo := split(repo)
	_, resp, err := client.Repositories.Edit(ctx, owner, repo, &r)
	if err != nil {
		return nil, fmt.Errorf("edit repo: %w", err)
	}
	return resp, nil
}

func branchesSync(ctx context.Context, client *github.Client, clientv4 *githubv4.Client, owner, repo string, branch string, setting Branches) error {
	var q struct {
		Repository struct {
			ID                    githubv4.ID
			BranchProtectionRules struct {
				Nodes []struct {
					ID      githubv4.ID
					Pattern githubv4.String
				}
				PageInfo struct {
					EndCursor   githubv4.String
					HasNextPage bool
				}
			} `graphql:"branchProtectionRules(first: 100, after: $cursor)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}
	input := map[string]interface{}{
		"owner":  githubv4.String(owner),
		"name":   githubv4.String(repo),
		"cursor": (*githubv4.String)(nil),
	}
	err := clientv4.Query(context.Background(), &q, input)
	if err != nil {
		return fmt.Errorf("get branch protection list: %w", err)
	}
	var exists githubv4.ID
	for _, rule := range q.Repository.BranchProtectionRules.Nodes {
		if rule.Pattern == githubv4.String(branch) {
			exists = rule.ID
		}
	}
	pattern := githubv4.NewString(githubv4.String(branch))
	if exists != nil {
		var c struct {
			UpdateBranchProtectionRule struct {
				BranchProtectionRule struct {
					ID      githubv4.ID
					Pattern githubv4.String
				}
			} `graphql:"updateBranchProtectionRule(input: $input)"`
		}
		cin := githubv4.UpdateBranchProtectionRuleInput{
			BranchProtectionRuleID: exists,
			Pattern:                pattern,
		}
		if setting.EnforceAdmins != nil {
			cin.IsAdminEnforced = (*githubv4.Boolean)(setting.EnforceAdmins)
		}
		if setting.DismissStaleReviews != nil {
			cin.DismissesStaleReviews = (*githubv4.Boolean)(setting.DismissStaleReviews)
		}
		if setting.RequiredApprovingReviewCount != nil {
			if *setting.RequiredApprovingReviewCount == 0 {
				cin.RequiresApprovingReviews = githubv4.NewBoolean(false)
				cin.RequiredApprovingReviewCount = githubv4.NewInt(0)
			} else {
				cin.RequiresApprovingReviews = githubv4.NewBoolean(true)
				cin.RequiredApprovingReviewCount = githubv4.NewInt(githubv4.Int(graphql.Int(*setting.RequiredApprovingReviewCount)))
			}
		}
		if setting.RequiredStatusChecks.Strict != nil {
			cin.RequiresStatusChecks = githubv4.NewBoolean(true)
			cin.RequiresStrictStatusChecks = (*githubv4.Boolean)(setting.RequiredStatusChecks.Strict)
		}
		if setting.RequiredStatusChecks.Content != nil {
			var v []githubv4.String
			for i := range setting.RequiredStatusChecks.Content {
				v = append(v, githubv4.String(setting.RequiredStatusChecks.Content[i]))
			}
			cin.RequiresStatusChecks = githubv4.NewBoolean(true)
			cin.RequiredStatusCheckContexts = &v
		}
		err = clientv4.Mutate(context.Background(), &c, cin, nil)
		if err != nil {
			return fmt.Errorf("update branch protection: %w", err)
		}
		log.Println("update branch protection", branch)
	} else {
		var c struct {
			CreateBranchProtectionRule struct {
				BranchProtectionRule struct {
					ID      githubv4.ID
					Pattern githubv4.String
				}
			} `graphql:"createBranchProtectionRule(input: $input)"`
		}
		cin := githubv4.CreateBranchProtectionRuleInput{
			RepositoryID: q.Repository.ID,
			Pattern:      *pattern,
		}
		if setting.EnforceAdmins != nil {
			cin.IsAdminEnforced = (*githubv4.Boolean)(setting.EnforceAdmins)
		}
		if setting.DismissStaleReviews != nil {
			cin.DismissesStaleReviews = (*githubv4.Boolean)(setting.DismissStaleReviews)
		}
		if setting.RequiredApprovingReviewCount != nil {
			cin.RequiredApprovingReviewCount = githubv4.NewInt(githubv4.Int(graphql.Int(*setting.RequiredApprovingReviewCount)))
		}
		if setting.RequiredStatusChecks.Strict != nil {
			cin.RequiresStrictStatusChecks = (*githubv4.Boolean)(setting.RequiredStatusChecks.Strict)
		}
		if setting.RequiredStatusChecks.Content != nil {
			var v []githubv4.String
			for i := range setting.RequiredStatusChecks.Content {
				v = append(v, githubv4.String(setting.RequiredStatusChecks.Content[i]))
			}
			cin.RequiredStatusCheckContexts = &v
		}
		err = clientv4.Mutate(context.Background(), &c, cin, nil)
		if err != nil {
			return fmt.Errorf("create branch protection: %w", err)
		}
		log.Println("create branch protection", branch)
	}
	return nil
}

func split(repo string) (string, string) {
	arr := strings.SplitN(repo, "/", 3)
	return arr[0], arr[1]
}

func limitWait(rate *github.Rate) {
	if rate.Remaining < 100 {
		d := time.Until(rate.Reset.Time) + time.Minute
		log.Println("limit wait", d)
		time.Sleep(d)
	}
}
