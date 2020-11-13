// Copyright (c) 2020, the Drone Plugins project authors.
// Please see the AUTHORS file for details. All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package plugin

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/google/go-github/v32/github"
)

// Release holds ties the drone env data and github client together.
type releaseClient struct {
	*github.Client
	context.Context
	Owner       string
	Repo        string
	Tag         string
	Draft       bool
	Prerelease  bool
	FileExists  string
	Title       string
	Note        string
	Overwrite   bool
	PickupDraft bool
}

func (rc *releaseClient) buildRelease() (*github.RepositoryRelease, error) {

	var release *github.RepositoryRelease
	var err error

	if rc.PickupDraft {
		release, err = rc.getReleaseDraft()
		if err != nil {
			return nil, err
		}
	}

	if release == nil {
		// first attempt to get a release by that tag
		release, err = rc.getRelease()
	}

	if err != nil && release == nil {
		fmt.Println(err)
		// if no release was found by that tag, create a new one
		release, err = rc.newRelease()
	} else if release != nil && rc.Overwrite {
		// update release if exists
		release, err = rc.editRelease(*release)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to retrieve or create a release: %w", err)
	}

	return release, nil
}

func (rc *releaseClient) getReleaseDraft() (*github.RepositoryRelease, error) {
	releaseList, _, err := rc.Client.Repositories.ListReleases(rc.Context, rc.Owner, rc.Repo, &github.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	dID := int64(-1)
	for _, rel := range releaseList {
		if rel.GetDraft() && rel.GetTagName() == rc.Tag {
			dID = rel.GetID()
			break
		}
	}
	if dID != -1 {
		releaseDraft, _, err := rc.Client.Repositories.GetRelease(rc.Context, rc.Owner, rc.Repo, dID)
		if err != nil {
			return nil, fmt.Errorf("failed to get release for ID %d: %w", dID, err)
		}
		return releaseDraft, nil
	}
	fmt.Println("No release draft found")
	return nil, nil
}

func (rc *releaseClient) getRelease() (*github.RepositoryRelease, error) {
	release, _, err := rc.Client.Repositories.GetReleaseByTag(rc.Context, rc.Owner, rc.Repo, rc.Tag)

	if err != nil {
		return nil, fmt.Errorf("release %s not found", rc.Tag)
	}

	fmt.Printf("Successfully retrieved %s release\n", rc.Tag)
	return release, nil
}

func (rc *releaseClient) editRelease(targetRelease github.RepositoryRelease) (*github.RepositoryRelease, error) {

	sourceRelease := &github.RepositoryRelease{
		Name: &rc.Title,
		Body: &rc.Note,
	}

	// only potentially change the draft value, if it's a draft right now
	// i.e. a drafted release will be published, but a release won't be unpublished
	if targetRelease.GetDraft() {
		if !rc.Draft {
			fmt.Println("Publishing a release draft")
		}
		sourceRelease.Draft = &rc.Draft
	}

	modifiedRelease, _, err := rc.Client.Repositories.EditRelease(rc.Context, rc.Owner, rc.Repo, targetRelease.GetID(), sourceRelease)

	if err != nil {
		return nil, fmt.Errorf("failed to update release: %w", err)
	}

	fmt.Printf("Successfully updated %s release\n", rc.Tag)
	return modifiedRelease, nil
}

func (rc *releaseClient) newRelease() (*github.RepositoryRelease, error) {
	rr := &github.RepositoryRelease{
		TagName:    github.String(rc.Tag),
		Draft:      &rc.Draft,
		Prerelease: &rc.Prerelease,
		Name:       &rc.Title,
		Body:       &rc.Note,
	}

	if *rr.Prerelease {
		fmt.Printf("Release %s identified as a pre-release\n", rc.Tag)
	} else {
		fmt.Printf("Release %s identified as a full release\n", rc.Tag)
	}

	if *rr.Draft {
		fmt.Printf("Release %s will be created as draft (unpublished) release\n", rc.Tag)
	} else {
		fmt.Printf("Release %s will be created and published\n", rc.Tag)
	}

	release, _, err := rc.Client.Repositories.CreateRelease(rc.Context, rc.Owner, rc.Repo, rr)

	if err != nil {
		return nil, fmt.Errorf("failed to create release: %w", err)
	}

	fmt.Printf("Successfully created %s release\n", rc.Tag)
	return release, nil
}

func (rc *releaseClient) uploadFiles(id int64, files []string) error {
	assets, _, err := rc.Client.Repositories.ListReleaseAssets(rc.Context, rc.Owner, rc.Repo, id, &github.ListOptions{})

	if err != nil {
		return fmt.Errorf("failed to fetch existing assets: %w", err)
	}

	var uploadFiles []string

files:
	for _, file := range files {
		for _, asset := range assets {
			if *asset.Name == path.Base(file) {
				switch rc.FileExists {
				case "overwrite":
					// do nothing
				case "fail":
					return fmt.Errorf("asset file %s already exists", path.Base(file))
				case "skip":
					fmt.Printf("Skipping pre-existing %s artifact\n", *asset.Name)
					continue files
				default:
					return fmt.Errorf("internal error, unknown file_exist value %s", rc.FileExists)
				}
			}
		}

		uploadFiles = append(uploadFiles, file)
	}

	for _, file := range uploadFiles {
		handle, err := os.Open(file)

		if err != nil {
			return fmt.Errorf("failed to read %s artifact: %w", file, err)
		}

		for _, asset := range assets {
			if *asset.Name == path.Base(file) {
				if _, err := rc.Client.Repositories.DeleteReleaseAsset(rc.Context, rc.Owner, rc.Repo, *asset.ID); err != nil {
					return fmt.Errorf("failed to delete %s artifact: %w", file, err)
				}

				fmt.Printf("Successfully deleted old %s artifact\n", *asset.Name)
			}
		}

		uo := &github.UploadOptions{Name: path.Base(file)}

		if _, _, err = rc.Client.Repositories.UploadReleaseAsset(rc.Context, rc.Owner, rc.Repo, id, uo, handle); err != nil {
			return fmt.Errorf("failed to upload %s artifact: %w", file, err)
		}

		fmt.Printf("Successfully uploaded %s artifact\n", file)
	}

	return nil
}
