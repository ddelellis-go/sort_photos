package main

import (
	_ "flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	_ "strings"
	_ "time"

	"gocv.io/x/gocv"
	"gocv.io/x/gocv/contrib"
	"gonum.org/v1/gonum/stat"
)

//TODO: create sort target dir
// if a new directory is needed, create a directory named after the first file in the set
// hardlink the file into the target directory

//TODO: flags
const (
	//sortDir = "/mnt/media/photos/sort/20250120_pleasantonridge/bird_branch_solo"
	sortDir = "/mnt/media/photos/.archive/f"
	//sortDir = "/mnt/media/photos/sort/20250111/jpg/test"
	RFC3339Micro     = "2006-01-02T15:04:05.999999"
	radVarMissMean   = 0.397
	radVarMissStdDev = 0.081
	momentMissMean   = 0.376
	momentMissStdDev = 0.173
	omissZScore      = -1.0
)

func validFile(f os.DirEntry) bool {
	info, err := f.Info()
	if err != nil {
		return false
	}
	if info.Size() < 1000.0 {
		return false
	}
	return true
}

func exifToolJpg(file string) (buf []byte, err error) {
	cmd := exec.Command("/usr/local/bin/exiftool", "-b", "-JpgFromRaw", file)
	fmt.Println("running", cmd.String())

	buf, err = cmd.Output()
	return
}

func findFiles(path string, d *os.File) (files []string, err error) {
	var dirList []os.DirEntry
	dirList, err = d.ReadDir(0)
	if err != nil {
		err = fmt.Errorf("failed listing contents of %s in %s: [%w]", d.Name(), path, err)
		return
	}
	for _, j := range dirList {
		fullPath := path + "/" + j.Name()
		if j.IsDir() {
			var subDir *os.File
			var subFiles []string
			subDir, err = os.Open(fullPath)
			if err != nil {
				err = fmt.Errorf("Failed opening subdir %s: [%w]", j.Name(), err)
				return
			}
			subFiles, err = findFiles(fullPath, subDir)
			if err != nil {
				err = fmt.Errorf("failed listing contents of subdir %s: [%w]", j.Name(), err)
				return
			}
			files = append(files, subFiles...)

		} else {
			if !validFile(j) {
				continue
			}
			files = append(files, fullPath)
		}
	}
	return
}

func main() {
	var ec int
	var msg error
	defer func() {
		if msg != nil {
			ec = 1
			fmt.Println(msg)
		}
		os.Exit(ec)
	}()
	workDir, dirErr := os.Open(sortDir)
	if dirErr != nil {
		msg = fmt.Errorf("Failed to open target directory: %w", dirErr)
		return
	}
	dirList, listErr := findFiles(sortDir, workDir)
	if listErr != nil {
		msg = fmt.Errorf("Failed to list target directory: %w", dirErr)
		return
	}

	filesMap := make(map[string]FileHashes)
	for _, v := range dirList {
		var temp FileHashes
		temp.Name = v

		filesMap[v] = temp
	}

	keys := slices.Sorted(maps.Keys(filesMap))
	HashA := hashA()
	HashB := hashB()

	var hitsA, hitsB []float64
	//    var missA, missB []float64

	for i, _ := range keys {
		k := filesMap[keys[i]]

		jpg, err := exifToolJpg(k.Name)
		if err != nil {
			msg = fmt.Errorf("failed running exiftool: %w", err)
			return
		}
		k.ImgMat, err = gocv.IMDecode(jpg, gocv.IMReadColor)
		if err != nil {
			msg = fmt.Errorf("failed decoding image: %w", err)
			return
		}
		//k.ImgMat = gocv.IMRead(k.Name, gocv.IMReadColor)
		//crashes here if the NEF file is from a rotated shot
		if k.ImgMat.Empty() {
			msg = fmt.Errorf("failed reading %s", k.Name)
			return
		}
		defer k.ImgMat.Close()

		k.ResA = gocv.NewMat()
		k.ResB = gocv.NewMat()
		defer k.ResA.Close()
		defer k.ResB.Close()

		HashA.Compute(k.ImgMat, &k.ResA)
		HashB.Compute(k.ImgMat, &k.ResB)

		filesMap[keys[i]] = k

		if i > 0 {
			prev := filesMap[keys[i-1]]
			simA := HashA.Compare(k.ResA, prev.ResA)
			simB := normalizeMoment(HashB.Compare(k.ResB, prev.ResB), 10)
			fmt.Println(keys[i], simB)

			newFolder := simB < 0.3

			//zA := stat.StdScore(simA, radVarMissMean, radVarMissStdDev)
			//zB := stat.StdScore(simB, momentMissMean, momentMissStdDev)
			if k.Name[0] == prev.Name[0] { //this means the pictures should be sorted together
				if newFolder {
					fmt.Println("** fuck")
				}
				hitsA = append(hitsA, simA)
				hitsB = append(hitsB, simB)
				//fmt.Println("keep, radVar:", zA > missZScore, "moment:", zB > missZScore, zA, zB)
			} else {
				if !newFolder {
					fmt.Println("dang")
				}
				hitsA = append(hitsA, simA)
				hitsB = append(hitsB, simB)
				//fmt.Println("new , radVar:", zA < missZScore, "moment:", zB < missZScore, zA, zB)
			}
		}
	}
	fmt.Println("radVar hits: mean:", stat.Mean(hitsA, nil), "var:", stat.Variance(hitsA, nil))
	fmt.Println("moment hits: mean:", stat.Mean(hitsB, nil), "var:", stat.Variance(hitsB, nil))
	/*
	   fmt.Println("radVar miss: mean:", stat.Mean(missA, nil), "var:", stat.Variance(missA, nil))
	   fmt.Println("moment miss: mean:", stat.Mean(missB, nil), "var:", stat.Variance(missB, nil))
	*/
}

func normalizeMoment(f float64, mid float64) float64 {
	return mid / (mid + f)
}

// TODO: radial variance and color moment both seem pretty good at detecting photos from the same set.
// radial variance is on (0-1) with 1 being most similar
// color moment is on [0-inf) 0 being an identical match and higher values meaning less similarity
// f(x) = a/x+a for f(a) = 0.5 will convert color moment into a number on [0-1), where 1 is most similar
// then take the product of the 2 and use it as a p score?
func hashList() []contrib.ImgHashBase {
	return []contrib.ImgHashBase{contrib.NewRadialVarianceHash(), contrib.ColorMomentHash{}}
}

func hashA() contrib.ImgHashBase {
	return contrib.NewRadialVarianceHash()
}

func hashB() contrib.ImgHashBase {
	return contrib.ColorMomentHash{}
}

type FileHashes struct {
	Name       string
	ImgMat     gocv.Mat
	ResA, ResB gocv.Mat
}
