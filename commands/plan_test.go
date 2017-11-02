package commands_test

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/cloudfoundry/bosh-bootloader/bosh"
	"github.com/cloudfoundry/bosh-bootloader/commands"
	"github.com/cloudfoundry/bosh-bootloader/fakes"
	"github.com/cloudfoundry/bosh-bootloader/storage"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan", func() {
	var (
		command commands.Plan

		boshManager        *fakes.BOSHManager
		terraformManager   *fakes.TerraformManager
		cloudConfigManager *fakes.CloudConfigManager
		stateStore         *fakes.StateStore
		envIDManager       *fakes.EnvIDManager

		tempDir string
	)

	BeforeEach(func() {
		boshManager = &fakes.BOSHManager{}
		boshManager.VersionCall.Returns.Version = "2.0.24"

		terraformManager = &fakes.TerraformManager{}
		cloudConfigManager = &fakes.CloudConfigManager{}
		stateStore = &fakes.StateStore{}
		envIDManager = &fakes.EnvIDManager{}

		var err error
		tempDir, err = ioutil.TempDir("", "")
		Expect(err).NotTo(HaveOccurred())

		stateStore.GetBblDirCall.Returns.Directory = tempDir

		command = commands.NewPlan(boshManager, cloudConfigManager, stateStore, envIDManager, terraformManager)
	})

	Describe("Execute", func() {
		var (
			state       storage.State
			syncedState storage.State
		)

		BeforeEach(func() {
			state = storage.State{ID: "some-state-id"}
			syncedState = storage.State{ID: "synced-state-id"}
			envIDManager.SyncCall.Returns.State = syncedState
		})

		It("sets up the bbl state dir", func() {
			args := []string{}
			err := command.Execute(args, state)
			Expect(err).NotTo(HaveOccurred())

			Expect(envIDManager.SyncCall.CallCount).To(Equal(1))
			Expect(envIDManager.SyncCall.Receives.State).To(Equal(state))

			Expect(stateStore.SetCall.CallCount).To(Equal(1))
			Expect(stateStore.SetCall.Receives[0].State).To(Equal(syncedState))

			Expect(terraformManager.InitCall.CallCount).To(Equal(1))
			Expect(terraformManager.InitCall.Receives.BBLState).To(Equal(syncedState))

			Expect(boshManager.InitializeJumpboxCall.CallCount).To(Equal(1))
			Expect(boshManager.InitializeJumpboxCall.Receives.State).To(Equal(syncedState))

			Expect(boshManager.InitializeDirectorCall.CallCount).To(Equal(1))
			Expect(boshManager.InitializeDirectorCall.Receives.State).To(Equal(syncedState))

			Expect(cloudConfigManager.InitializeCall.CallCount).To(Equal(1))
			Expect(cloudConfigManager.InitializeCall.Receives.State).To(Equal(syncedState))
		})

		Context("when --no-director is passed", func() {
			It("sets no director on the state", func() {
				envIDManager.SyncCall.Returns.State = storage.State{NoDirector: true}

				err := command.Execute([]string{"--no-director"}, storage.State{NoDirector: false})
				Expect(err).NotTo(HaveOccurred())

				Expect(boshManager.InitializeJumpboxCall.CallCount).To(Equal(0))
				Expect(boshManager.InitializeDirectorCall.CallCount).To(Equal(0))
			})

			Context("but a director already exists", func() {
				It("returns a helpful error", func() {
					err := command.Execute([]string{"--no-director"}, storage.State{
						BOSH: storage.BOSH{
							DirectorUsername: "admin",
						},
					})
					Expect(err).To(MatchError(`Director already exists, you must re-create your environment to use "--no-director"`))
				})
			})
		})

		Describe("failure cases", func() {
			It("returns an error if state store set fails", func() {
				stateStore.SetCall.Returns = []fakes.SetCallReturn{{Error: errors.New("peach")}}

				err := command.Execute([]string{}, storage.State{})
				Expect(err).To(MatchError("Save state: peach"))
			})

			It("returns an error if terraform manager init fails", func() {
				terraformManager.InitCall.Returns.Error = errors.New("pomegranate")

				err := command.Execute([]string{}, storage.State{})
				Expect(err).To(MatchError("Terraform manager init: pomegranate"))
			})

			It("returns an error if bosh manager initialize jumpbox fails", func() {
				boshManager.InitializeJumpboxCall.Returns.Error = errors.New("tomato")

				err := command.Execute([]string{}, storage.State{})
				Expect(err).To(MatchError("Bosh manager initialize jumpbox: tomato"))
			})

			It("returns an error if bosh manager initialize director fails", func() {
				boshManager.InitializeDirectorCall.Returns.Error = errors.New("tomatoe")

				err := command.Execute([]string{}, storage.State{})
				Expect(err).To(MatchError("Bosh manager initialize director: tomatoe"))
			})

			It("returns an error if cloud config initialize fails", func() {
				cloudConfigManager.InitializeCall.Returns.Error = errors.New("potato")

				err := command.Execute([]string{}, storage.State{})
				Expect(err).To(MatchError("Cloud config manager initialize: potato"))
			})
		})
	})

	Describe("CheckFastFails", func() {
		Context("when terraform manager validate version fails", func() {
			It("returns an error", func() {
				terraformManager.ValidateVersionCall.Returns.Error = errors.New("lychee")

				err := command.CheckFastFails([]string{}, storage.State{})
				Expect(err).To(MatchError("Terraform manager validate version: lychee"))
			})
		})

		Context("when the version of BOSH is a dev build", func() {
			It("does not fail", func() {
				boshManager.VersionCall.Returns.Error = bosh.NewBOSHVersionError(errors.New("BOSH version could not be parsed"))
				err := command.CheckFastFails([]string{}, storage.State{Version: 999})

				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("when the version of the bosh-cli is lower than 2.0.24", func() {
			Context("when there is a bosh director", func() {
				It("returns an error", func() {
					boshManager.VersionCall.Returns.Version = "1.9.1"
					err := command.CheckFastFails([]string{}, storage.State{Version: 999})

					Expect(err).To(MatchError("BOSH version must be at least v2.0.24"))
				})
			})

			Context("when there is no director", func() {
				It("does not return an error", func() {
					boshManager.VersionCall.Returns.Version = "1.9.1"
					err := command.CheckFastFails([]string{"--no-director"}, storage.State{Version: 999})

					Expect(err).NotTo(HaveOccurred())
				})
			})
		})

		Context("when bosh -v fails", func() {
			It("returns an error", func() {
				boshManager.VersionCall.Returns.Error = errors.New("BOOM")
				err := command.CheckFastFails([]string{}, storage.State{Version: 999})

				Expect(err.Error()).To(ContainSubstring("BOOM"))
			})
		})

		Context("when bosh -v is invalid", func() {
			It("returns an error", func() {
				boshManager.VersionCall.Returns.Version = "X.5.2"
				err := command.CheckFastFails([]string{}, storage.State{Version: 999})

				Expect(err.Error()).To(ContainSubstring("invalid syntax"))
			})
		})

		Context("when bbl-state contains an env-id", func() {
			Context("when the passed in name matches the env-id", func() {
				It("returns no error", func() {
					err := command.CheckFastFails([]string{
						"--name", "some-name",
					}, storage.State{EnvID: "some-name"})
					Expect(err).NotTo(HaveOccurred())
				})
			})

			Context("when the passed in name does not match the env-id", func() {
				It("returns an error", func() {
					err := command.CheckFastFails([]string{
						"--name", "some-other-name",
					}, storage.State{EnvID: "some-name"})
					Expect(err).To(MatchError("The director name cannot be changed for an existing environment. Current name is some-name."))
				})
			})
		})
	})

	Describe("ParseArgs", func() {
		Context("when the --ops-file flag is specified", func() {
			var providedOpsFilePath string
			BeforeEach(func() {
				opsFileDir, err := ioutil.TempDir("", "")
				Expect(err).NotTo(HaveOccurred())

				providedOpsFilePath = filepath.Join(opsFileDir, "some-ops-file")

				err = ioutil.WriteFile(providedOpsFilePath, []byte("some-ops-file-contents"), os.ModePerm)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns a config with the ops-file path", func() {
				config, err := command.ParseArgs([]string{
					"--ops-file", providedOpsFilePath,
				}, storage.State{})
				Expect(err).NotTo(HaveOccurred())

				Expect(config.OpsFile).To(Equal(providedOpsFilePath))
			})
		})

		Context("when the --ops-file flag is not specified", func() {
			It("creates a default ops-file with the contents of state.BOSH.UserOpsFile", func() {
				config, err := command.ParseArgs([]string{}, storage.State{
					BOSH: storage.BOSH{
						UserOpsFile: "some-ops-file-contents",
					},
				})
				Expect(err).NotTo(HaveOccurred())

				filePath := config.OpsFile
				fileContents, err := ioutil.ReadFile(filePath)
				Expect(err).NotTo(HaveOccurred())

				Expect(string(fileContents)).To(Equal("some-ops-file-contents"))
			})

			It("writes the previous user ops file to the .bbl directory", func() {
				config, err := command.ParseArgs([]string{}, storage.State{
					BOSH: storage.BOSH{
						UserOpsFile: "some-ops-file-contents",
					},
				})
				Expect(err).NotTo(HaveOccurred())

				filePath := config.OpsFile
				fileContents, err := ioutil.ReadFile(filePath)
				Expect(err).NotTo(HaveOccurred())

				Expect(filePath).To(Equal(filepath.Join(tempDir, "previous-user-ops-file.yml")))
				Expect(string(fileContents)).To(Equal("some-ops-file-contents"))
			})
		})

		Context("when the user provides the name flag", func() {
			It("passes the name flag in the up config", func() {
				config, err := command.ParseArgs([]string{
					"--name", "a-better-name",
				}, storage.State{})
				Expect(err).NotTo(HaveOccurred())
				Expect(config.Name).To(Equal("a-better-name"))
			})
		})

		Context("when the user provides the no-director flag", func() {
			It("passes NoDirector as true in the up config", func() {
				config, err := command.ParseArgs([]string{
					"--no-director",
				}, storage.State{})
				Expect(err).NotTo(HaveOccurred())
				Expect(config.NoDirector).To(Equal(true))
			})

			Context("when the --no-director flag was omitted on a subsequent bbl-up", func() {
				It("passes no-director as true in the up config", func() {
					config, err := command.ParseArgs([]string{},
						storage.State{
							IAAS:       "gcp",
							NoDirector: true,
						})
					Expect(err).NotTo(HaveOccurred())
					Expect(config.NoDirector).To(Equal(true))
				})
			})
		})

		Context("failure cases", func() {
			Context("when undefined flags are passed", func() {
				It("returns an error", func() {
					_, err := command.ParseArgs([]string{"--foo", "bar"}, storage.State{})
					Expect(err).To(MatchError("flag provided but not defined: -foo"))
				})
			})
		})
	})
})