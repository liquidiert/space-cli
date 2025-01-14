package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"

	"github.com/deta/space/cmd/shared"
	"github.com/deta/space/internal/api"
	"github.com/deta/space/internal/auth"
	"github.com/deta/space/internal/runtime"
	"github.com/deta/space/pkg/components/choose"
	"github.com/deta/space/pkg/components/confirm"
	"github.com/deta/space/pkg/components/emoji"
	"github.com/deta/space/pkg/components/styles"
	"github.com/spf13/cobra"
)

const (
	ReleaseChannelExp = "experimental"
)

func newCmdRelease() *cobra.Command {
	cmd := &cobra.Command{
		Use:      "release [flags]",
		Short:    "Create a new release from a revision",
		PreRunE:  shared.CheckAll(shared.CheckProjectInitialized("dir"), shared.CheckNotEmpty("id", "rid", "version")),
		PostRunE: shared.CheckLatestVersion,
		Run: func(cmd *cobra.Command, args []string) {
			var err error

			if !shared.IsOutputInteractive() && !cmd.Flags().Changed("rid") && !cmd.Flags().Changed("confirm") {
				shared.Logger.Printf("revision id or confirm flag must be provided in non-interactive mode")
				os.Exit(1)
			}

			projectDir, _ := cmd.Flags().GetString("dir")
			projectID, _ := cmd.Flags().GetString("id")
			releaseNotes, _ := cmd.Flags().GetString("notes")
			revisionID, _ := cmd.Flags().GetString("rid")
			useLatestRevision, _ := cmd.Flags().GetBool("confirm")
			listedRelease, _ := cmd.Flags().GetBool("listed")
			releaseVersion, _ := cmd.Flags().GetString("version")

			if !cmd.Flags().Changed("id") {
				projectMeta, err := runtime.GetProjectMeta(projectDir)
				if err != nil {
					os.Exit(1)
				}
				projectID = projectMeta.ID
			}

			if !cmd.Flags().Changed("rid") {
				if !cmd.Flags().Changed("confirm") {
					useLatestRevision, err = confirm.Run("Do you want to use the latest revision?")
					if err != nil {
						os.Exit(1)
					}
				}

				revision, err := selectRevision(projectID, useLatestRevision)
				if err != nil {
					os.Exit(1)
				}
				shared.Logger.Printf("\nSelected revision: %s", styles.Blue(revision.Tag))

				revisionID = revision.ID

			}

			shared.Logger.Printf(getCreatingReleaseMsg(listedRelease, useLatestRevision))
			if err := release(projectDir, projectID, revisionID, releaseVersion, listedRelease, releaseNotes); err != nil {
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringP("dir", "d", "./", "src of project to release")
	cmd.Flags().StringP("id", "i", "", "project id of an existing project")
	cmd.Flags().String("rid", "", "revision id for release")
	cmd.Flags().StringP("version", "v", "", "version for the release")
	cmd.Flags().Bool("listed", false, "listed on discovery")
	cmd.Flags().Bool("confirm", false, "confirm to use latest revision")
	cmd.Flags().StringP("notes", "n", "", "release notes")

	cmd.MarkFlagsMutuallyExclusive("confirm", "rid")

	return cmd
}

func selectRevision(projectID string, useLatestRevision bool) (*api.Revision, error) {
	r, err := shared.Client.GetRevisions(&api.GetRevisionsRequest{ID: projectID})
	if err != nil {
		if errors.Is(err, auth.ErrNoAccessTokenFound) {
			shared.Logger.Println(shared.LoginInfo())
			return nil, err
		} else {
			shared.Logger.Println(styles.Errorf("%s Failed to get revisions: %v", emoji.ErrorExclamation, err))
			return nil, err
		}
	}
	revisions := r.Revisions

	if len(r.Revisions) == 0 {
		shared.Logger.Printf(styles.Errorf("%s No revisions found. Please create a revision by running %s", emoji.ErrorExclamation, styles.Code("space push")))
		return nil, err
	}

	latestRevision := r.Revisions[0]
	if useLatestRevision {
		return latestRevision, nil
	}
	tags := []string{}
	if len(revisions) > 5 {
		revisions = revisions[:5]
	}

	revisionMap := make(map[string]*api.Revision)
	for _, revision := range revisions {
		revisionMap[revision.Tag] = revision
		tags = append(tags, revision.Tag)
	}

	tag, err := choose.Run(
		fmt.Sprintf("Choose a revision %s:", styles.Subtle("(most recent revisions)")),
		tags...,
	)
	if err != nil {
		return nil, err
	}

	return revisionMap[tag], nil
}

func release(projectDir string, projectID string, revisionID string, releaseVersion string, listedRelease bool, releaseNotes string) (err error) {
	cr, err := shared.Client.CreateRelease(&api.CreateReleaseRequest{
		RevisionID:    revisionID,
		AppID:         projectID,
		Version:       releaseVersion,
		ReleaseNotes:  releaseNotes,
		DiscoveryList: listedRelease,
		Channel:       ReleaseChannelExp, // always experimental release for now
	})
	if err != nil {
		if errors.Is(err, auth.ErrNoAccessTokenFound) {
			shared.Logger.Println(shared.LoginInfo())
			return nil
		}
		shared.Logger.Println(styles.Errorf("%s Failed to create release: %v", emoji.ErrorExclamation, err))
		return err
	}
	readCloser, err := shared.Client.GetReleaseLogs(&api.GetReleaseLogsRequest{
		ID: cr.ID,
	})
	if err != nil {
		shared.Logger.Println(styles.Errorf("%s Error: %v", emoji.ErrorExclamation, err))
		return err
	}

	defer readCloser.Close()
	scanner := bufio.NewScanner(readCloser)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)
	}
	if err := scanner.Err(); err != nil {
		shared.Logger.Printf("%s Error: %v\n", emoji.ErrorExclamation, err)
		return err
	}

	r, err := shared.Client.GetReleasePromotion(&api.GetReleasePromotionRequest{PromotionID: cr.ID})
	if err != nil {
		shared.Logger.Printf(styles.Errorf("\n%s Failed to check if release succeeded. Please check %s if a new release was created successfully.", emoji.ErrorExclamation, styles.Codef("%s/%s/develop", shared.BuilderUrl, projectID)))
		return err
	}

	if r.Status == api.Complete {
		shared.Logger.Println()
		shared.Logger.Println(emoji.Rocket, "Lift off -- successfully created a new Release!")
		shared.Logger.Println(emoji.Earth, "Your Release is available globally on 5 Deta Edges")
		shared.Logger.Println(emoji.PartyFace, "Anyone can install their own copy of your app.")
		if listedRelease {
			shared.Logger.Println(emoji.CrystalBall, "Listed on Discovery for others to find!")
		}
	} else {
		shared.Logger.Println(styles.Errorf("\n%s Failed to create release. Please try again!", emoji.ErrorExclamation))
		return fmt.Errorf("release failed: %s", r.Status)
	}

	return nil
}

func getCreatingReleaseMsg(listed bool, latest bool) string {
	var listedInfo string
	var latestInfo string
	if listed {
		listedInfo = " listed"
	}
	if latest {
		latestInfo = " with the latest Revision"
	}
	return fmt.Sprintf("\n%s Creating a%s Release%s ...\n\n", emoji.Package, listedInfo, latestInfo)
}
