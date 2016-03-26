package github

import (
	"errors"
	"log"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/golang/groupcache/lru"
	"github.com/google/go-github/github"
)

// StarTracker keeps track of how many stars a repository has. It keeps a huge
// in-memory LRU, goes to GitHub for never-seen-before, and it assumes it will
// be told about every WatchEvent ever since so that it can keep the number
// accurate without ever going to the network again.
type StarTracker struct {
	lru *lru.Cache
	gh  *github.Client

	panicIfNetwork bool // used for testing
}

type repo struct {
	stars       int
	parent      string
	lastFetched time.Time
}

func NewStarTracker(maxSize int, gitHubToken string) *StarTracker {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: gitHubToken},
	)
	tc := oauth2.NewClient(oauth2.NoContext, ts)

	return &StarTracker{lru: lru.New(maxSize), gh: github.NewClient(tc)}
}

func (s *StarTracker) Get(name string) (stars int, parent string, err error) {
	res, ok := s.lru.Get(name)
	if ok {
		repo := res.(*repo)
		return repo.stars, repo.parent, nil
	}

	if s.panicIfNetwork {
		panic("network connection with panicIfNetwork=true")
	}

	nameParts := strings.Split(name, "/")
	if len(nameParts) != 2 {
		return 0, "", errors.New("name must be in user/repo format")
	}
	for {
		t := time.Now()
		r, _, err := s.gh.Repositories.Get(nameParts[0], nameParts[1])
		if err, ok := err.(*github.RateLimitError); ok {
			log.Println("Hit GitHub ratelimits, sleeping until", err.Rate.Reset)
			time.Sleep(err.Rate.Reset.Sub(time.Now()))
			continue
		}
		if err != nil {
			return 0, "", err
		}
		if r.StargazersCount == nil {
			return 0, "", errors.New("GitHub didn't tell us the StargazersCount")
		}

		if r.Parent != nil && r.Parent.FullName != nil {
			parent = *r.Parent.FullName
		}

		s.lru.Add(name, &repo{
			stars:       *r.StargazersCount,
			lastFetched: t,
			parent:      parent,
		})
		return *r.StargazersCount, parent, nil
	}
}

func (s *StarTracker) WatchEvent(name string, created time.Time) {
	res, ok := s.lru.Get(name)
	if !ok {
		return
	}

	repo := res.(*repo)
	if created.After(repo.lastFetched) {
		repo.stars += 1
	}
}

func (s *StarTracker) CreateEvent(name, parent string, created time.Time) {
	if _, ok := s.lru.Get(name); !ok {
		s.lru.Add(name, &repo{
			stars:       0,
			lastFetched: created,
			parent:      parent,
		})
	} else {
		log.Println("CreateEvent after we already knew about the repo:", name)
	}
}

func Is404(err error) bool {
	if err, ok := err.(*github.ErrorResponse); ok {
		if err.Response != nil && err.Response.StatusCode == 404 {
			return true
		}
	}
	return false
}
