package download

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/52funny/pikpakcli/conf"
	"github.com/52funny/pikpakcli/internal/pikpak"
	"github.com/52funny/pikpakcli/internal/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var DownloadCmd = &cobra.Command{
	Use:     "download",
	Aliases: []string{"d"},
	Short:   `Download file from pikpak server`,
	Run: func(cmd *cobra.Command, args []string) {
		p := pikpak.NewPikPak(conf.Config.Username, conf.Config.Password)
		err := p.Login()
		if err != nil {
			logrus.Errorln("Login Failed:", err)
			os.Exit(1)
		}
		if len(args) > 0 {
			err := downloadFile(&p, args)
			if err != nil {
				os.Exit(2)
			}
		} else {
			err := downloadFolder(&p)
			if err != nil {
				os.Exit(2)
			}
		}
	},
}

// Number of simultaneous downloads
//
// default 1
var count int

// Specifies the folder of the pikpak server
//
// default server root directory (.)
var folder string

// parent path id
var parentId string

// Output directory
//
// default current directory (.)
var output string

type warpFile struct {
	f      *pikpak.File
	output string
}

type warpStat struct {
	s      pikpak.FileStat
	output string
}

func init() {
	DownloadCmd.Flags().IntVarP(&count, "count", "c", 1, "number of simultaneous downloads")
	DownloadCmd.Flags().StringVarP(&output, "output", "o", "", "output directory")
	DownloadCmd.Flags().StringVarP(&folder, "path", "p", "/", "specific the folder of the pikpak server\nonly support download folder")
	DownloadCmd.Flags().StringVarP(&parentId, "parent-id", "P", "", "the parent path id")
}

// Downloads all files in the specified directory
func downloadFolder(p *pikpak.PikPak) error {
	base := filepath.Base(folder)
	var err error
	if parentId == "" {
		parentId, err = p.GetPathFolderId(folder)
		if err != nil {
			logrus.Errorln("Get Parent Folder Id Failed:", err)
			return errors.New("error")
		}

	}
	collectStat := make([]warpStat, 0)
	recursive(p, &collectStat, parentId, filepath.Join(output, base))

	statCh := make(chan warpStat, len(collectStat))
	statDone := make(chan struct{})

	fileCh := make(chan warpFile, len(collectStat))
	fileDone := make(chan struct{})

	for i := 0; i < 4; i += 1 {
		go func(fileCh chan<- warpFile, statCh <-chan warpStat, statDone chan<- struct{}) {
			for {
				stat, ok := <-statCh
				if !ok {
					break
				}
				file, err := p.GetFile(stat.s.ID)
				if err != nil {
					logrus.Errorln("Get File Failed:", err)
				}
				fileCh <- warpFile{
					f:      &file,
					output: stat.output,
				}
				statDone <- struct{}{}
			}
		}(fileCh, statCh, statDone)
	}

	for i := 0; i < count; i++ {
		go download(fileCh, fileDone)
	}

	for i := 0; i < len(collectStat); i += 1 {
		err := utils.CreateDirIfNotExist(collectStat[i].output)
		if err != nil {
			logrus.Errorln("Create output directory failed:", err)
			return errors.New("error")
		}
		statCh <- collectStat[i]
	}
	close(statCh)

	for i := 0; i < len(collectStat); i += 1 {
		<-statDone
	}
	close(statDone)

	for i := 0; i < len(collectStat); i += 1 {
		<-fileDone
	}
	return nil
}

func recursive(p *pikpak.PikPak, collectWarpFile *[]warpStat, parentId string, parentPath string) {
	statList, err := p.GetFolderFileStatList(parentId)
	if err != nil {
		logrus.Errorln("Get Folder File Stat List Failed:", err)
		return
	}
	for _, r := range statList {
		if r.Kind == "drive#folder" {
			recursive(p, collectWarpFile, r.ID, filepath.Join(parentPath, r.Name))
		} else {
			// file, _ := p.GetFile(r.ID)
			*collectWarpFile = append(*collectWarpFile, warpStat{
				s:      r,
				output: parentPath,
			})
			// fmt.Println(r.Name, r.Size, r.Kind, parentPath)
		}
	}
}

func downloadFile(p *pikpak.PikPak, args []string) error {
	var err error
	if parentId == "" {
		parentId, err = p.GetPathFolderId(folder)
		if err != nil {
			logrus.Errorln("get folder failed:", err)
			return errors.New("error")
		}
	}

	// if output not exists then create.
	if err := utils.CreateDirIfNotExist(output); err != nil {
		logrus.Errorln("Create output directory failed:", err)
		return errors.New("error")
	}

	sendCh := make(chan warpFile, 1)
	receiveCh := make(chan struct{}, len(args))

	for i := 0; i < count; i++ {
		go download(sendCh, receiveCh)
	}
	for _, path := range args {
		stat, err := p.GetFileStat(parentId, path)
		if err != nil {
			logrus.Errorln(path, "get parent id failed:", err)
			continue
		}

		file, err := p.GetFile(stat.ID)
		if err != nil {
			logrus.Errorln(path, "get file failed", err)
			continue
		}
		sendCh <- warpFile{
			f:      &file,
			output: output,
		}
	}
	close(sendCh)
	for i := 0; i < len(args); i++ {
		<-receiveCh
	}
	close(receiveCh)
	return nil
}

func download(inCh <-chan warpFile, out chan<- struct{}) {
	for {
		warp, ok := <-inCh
		if !ok {
			break
		}
		path := filepath.Join(warp.output, warp.f.Name)
		exist, err := utils.Exists(path)
		if err != nil {
			logrus.Errorln("Access", path, "Failed:", err)
			out <- struct{}{}
			continue
		}
		flag := path + ".pikpakclidownload"
		hasFlag, err := utils.Exists(flag)
		if err != nil {
			logrus.Errorln("Access", flag, "Failed:", err)
			out <- struct{}{}
			continue
		}
		if exist && !hasFlag {
			logrus.Infoln("Skip downloaded file", warp.f.Name)
			out <- struct{}{}
			continue
		}
		err = utils.TouchFile(flag)
		if err != nil {
			logrus.Errorln("Create flag file", flag, "Failed:", err)
			out <- struct{}{}
			continue
		}
		err = warp.f.Download(path)
		if err != nil {
			logrus.Errorln("Download", warp.f.Name, "Failed:", err)
		} else {
			logrus.Infoln("Download", warp.f.Name, "Success")
			err = os.Remove(flag)
			if err != nil {
				logrus.Warnln("Remove flag file", flag, "Failed:", err)
			}
		}
		out <- struct{}{}
	}
}
