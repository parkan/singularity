package datasource

import (
	"context"
	"strings"

	"github.com/data-preservation-programs/singularity/database"
	"github.com/data-preservation-programs/singularity/datasource"
	"github.com/data-preservation-programs/singularity/model"
	"github.com/data-preservation-programs/singularity/pack"
	"github.com/data-preservation-programs/singularity/pack/daggen"
	"github.com/data-preservation-programs/singularity/pack/device"
	"github.com/data-preservation-programs/singularity/util"
	"github.com/ipfs/go-cid"
	format "github.com/ipfs/go-ipld-format"
	"github.com/pkg/errors"
	"github.com/rjNemo/underscore"
	"gorm.io/gorm"
)

func PackHandler(
	db *gorm.DB,
	ctx context.Context,
	resolver datasource.HandlerResolver,
	packJobID uint64,
) ([]model.Car, error) {
	return packHandler(db, ctx, resolver, packJobID)
}

// @Summary Pack a pack job into car files
// @Tags Data Source
// @Accept json
// @Produce json
// @Param id path string true "Pack job ID"
// @Success 201 {object} []model.Car
// @Failure 400 {string} string "Bad Request"
// @Failure 500 {string} string "Internal Server Error"
// @Router /packjob/{id}/pack [post]
func packHandler(
	db *gorm.DB,
	ctx context.Context,
	resolver datasource.HandlerResolver,
	packJobID uint64,
) ([]model.Car, error) {
	var packJob model.PackJob
	err := db.Where("id = ?", packJobID).Find(&packJob).Error
	if err != nil {
		return nil, err
	}

	return Pack(ctx, db, packJob, resolver)
}

func Pack(
	ctx context.Context,
	db *gorm.DB,
	packJob model.PackJob,
	resolver datasource.HandlerResolver,
) ([]model.Car, error) {
	var outDir string
	if len(packJob.Source.Dataset.OutputDirs) > 0 {
		var err error
		outDir, err = device.GetPathWithMostSpace(packJob.Source.Dataset.OutputDirs)
		if err != nil {
			logger.Warnw("failed to get path with most space. using the first one", "error", err)
			outDir = packJob.Source.Dataset.OutputDirs[0]
		}
	}
	logger.Debugw("Use output dir", "dir", outDir)
	handler, err := resolver.Resolve(ctx, *packJob.Source)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get datasource handler")
	}
	result, err := pack.AssembleCar(ctx, handler, *packJob.Source.Dataset,
		packJob.FileRanges, outDir, packJob.Source.Dataset.PieceSize)
	if err != nil {
		return nil, errors.Wrap(err, "failed to pack files")
	}

	for _, fileRange := range packJob.FileRanges {
		fileRangeID := fileRange.ID
		fileRangeCID, ok := result.FileRangeCIDs[fileRangeID]
		if !ok {
			return nil, errors.New("file part not found in result")
		}
		logger.Debugw("update file part CID", "fileRangeID", fileRangeID, "CID", fileRangeCID.String())
		err = database.DoRetry(ctx, func() error {
			return db.Model(&model.FileRange{}).Where("id = ?", fileRangeID).
				Update("cid", model.CID(fileRangeCID)).Error
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to update cid of file")
		}
		logger.Debugw("update file CID", "fileID", fileRange.FileID, "CID", fileRangeCID.String())
		if fileRange.Offset == 0 && fileRange.Length == fileRange.File.Size {
			err = database.DoRetry(ctx, func() error {
				return db.Model(&model.File{}).Where("id = ?", fileRange.FileID).
					Update("cid", model.CID(fileRangeCID)).Error
			})
			if err != nil {
				return nil, errors.Wrap(err, "failed to update cid of file")
			}
		}
	}

	logger.Debugw("create car for finished pack job", "packJobID", packJob.ID)
	var cars []model.Car
	err = database.DoRetry(ctx, func() error {
		return db.Transaction(
			func(db *gorm.DB) error {
				for _, result := range result.CarResults {
					car := model.Car{
						PieceCID:  model.CID(result.PieceCID),
						PieceSize: result.PieceSize,
						RootCID:   model.CID(result.RootCID),
						FileSize:  result.CarFileSize,
						FilePath:  result.CarFilePath,
						PackJobID: &packJob.ID,
						DatasetID: packJob.Source.DatasetID,
						SourceID:  &packJob.SourceID,
						Header:    result.Header,
					}
					err := db.Create(&car).Error
					if err != nil {
						return errors.Wrap(err, "failed to create car")
					}
					for i := range result.CarBlocks {
						result.CarBlocks[i].CarID = car.ID
					}
					err = db.CreateInBatches(&result.CarBlocks, util.BatchSize).Error
					if err != nil {
						return errors.Wrap(err, "failed to create car blocks")
					}
					cars = append(cars, car)
				}
				return nil
			},
		)
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to save car")
	}

	logger.Debugw("update directory data", "packJobID", packJob.ID)
	err = database.DoRetry(ctx, func() error {
		return db.Transaction(func(db *gorm.DB) error {
			dirCache := make(map[uint64]*daggen.DirectoryData)
			childrenCache := make(map[uint64][]uint64)
			for _, fileRange := range packJob.FileRanges {
				dirID := fileRange.File.DirectoryID
				for dirID != nil {
					dirData, ok := dirCache[*dirID]
					if !ok {
						dirData = &daggen.DirectoryData{}
						var dir model.Directory
						err := db.Where("id = ?", dirID).First(&dir).Error
						if err != nil {
							return errors.Wrap(err, "failed to get directory")
						}

						err = dirData.UnmarshallBinary(dir.Data)
						if err != nil {
							return errors.Wrap(err, "failed to unmarshall directory data")
						}
						dirData.Directory = dir
						dirCache[*dirID] = dirData
						if dir.ParentID != nil {
							childrenCache[*dir.ParentID] = append(childrenCache[*dir.ParentID], *dirID)
						}
					}

					// Update the directory for first iteration
					if dirID == fileRange.File.DirectoryID {
						fileRangeID := fileRange.ID
						fileRangeCID, ok := result.FileRangeCIDs[fileRangeID]
						if !ok {
							return errors.New("file part not found in result")
						}
						err = db.Model(&model.FileRange{}).Where("id = ?", fileRangeID).
							Update("cid", model.CID(fileRangeCID)).Error
						if err != nil {
							return errors.Wrap(err, "failed to update cid of file")
						}
						name := fileRange.File.Path[strings.LastIndex(fileRange.File.Path, "/")+1:]
						if fileRange.Offset == 0 && fileRange.Length == fileRange.File.Size {
							partCID := result.FileRangeCIDs[fileRange.ID]
							err = dirData.AddFile(name, partCID, uint64(fileRange.Length))
							if err != nil {
								return errors.Wrap(err, "failed to add file to directory")
							}
							/*
								err = db.Model(&model.File{}).Where("id = ?", fileRange.FileID).Update("cid", model.CID(fileRangeCID)).Error
								if err != nil {
									return errors.Wrap(err, "failed to update cid of file")
								}
							*/
						} else {
							var allParts []model.FileRange
							err = db.Where("file_id = ?", fileRange.FileID).Order("\"offset\" asc").Find(&allParts).Error
							if err != nil {
								return errors.Wrap(err, "failed to get all file parts")
							}
							if underscore.All(allParts, func(p model.FileRange) bool {
								return p.CID != model.CID(cid.Undef)
							}) {
								links := underscore.Map(allParts, func(p model.FileRange) format.Link {
									return format.Link{
										Size: uint64(p.Length),
										Cid:  cid.Cid(p.CID),
									}
								})
								c, err := dirData.AddFileFromLinks(name, links)
								if err != nil {
									return errors.Wrap(err, "failed to add file to directory")
								}
								err = db.Model(&model.File{}).Where("id = ?", fileRange.FileID).Update("cid", model.CID(c)).Error
								if err != nil {
									return errors.Wrap(err, "failed to update cid of file")
								}
							}
						}
					}

					// Next iteration
					dirID = dirData.Directory.ParentID
				}
			}
			// Recursively update all directory internal structure
			rootDirID, err := packJob.Source.RootDirectoryID(db)
			if err != nil {
				return errors.Wrap(err, "failed to get root directory id")
			}
			_, err = daggen.ResolveDirectoryTree(rootDirID, dirCache, childrenCache)
			if err != nil {
				return errors.Wrap(err, "failed to resolve directory tree")
			}
			// Update all directories in the database
			for dirID, dirData := range dirCache {
				bytes, err := dirData.MarshalBinary()
				if err != nil {
					return errors.Wrap(err, "failed to marshall directory data")
				}
				err = db.Model(&model.Directory{}).Where("id = ?", dirID).Updates(map[string]any{
					"cid":      model.CID(dirData.Node.Cid()),
					"data":     bytes,
					"exported": false,
				}).Error
				if err != nil {
					return errors.Wrap(err, "failed to update directory")
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to update directory CIDs")
	}

	logger.With("pack_job_id", packJob.ID).Info("finished packing")
	if packJob.Source.DeleteAfterExport && result.CarResults[0].CarFilePath != "" {
		logger.Info("Deleting original data source")
		handled := map[uint64]struct{}{}
		for _, fileRange := range packJob.FileRanges {
			if _, ok := handled[fileRange.FileID]; ok {
				continue
			}
			handled[fileRange.FileID] = struct{}{}
			object := result.Objects[fileRange.FileID]
			if fileRange.Offset == 0 && fileRange.Length == fileRange.File.Size {
				logger.Debugw("removing object", "path", object.Remote())
				err = object.Remove(ctx)
				if err != nil {
					logger.Warnw("failed to remove object", "error", err)
				}
				continue
			}
			// Make sure all parts of this file has been exported before deleting
			var unfinishedCount int64
			err = db.Model(&model.FileRange{}).
				Where("file_id = ? AND cid IS NULL", fileRange.FileID).Count(&unfinishedCount).Error
			if err != nil {
				logger.Warnw("failed to get count for unfinished file parts", "error", err)
				continue
			}
			if unfinishedCount > 0 {
				logger.Info("not all files have been exported yet, skipping delete")
				continue
			}
			logger.Debugw("removing object", "path", object.Remote())
			err = object.Remove(ctx)
			if err != nil {
				logger.Warnw("failed to remove object", "error", err)
			}
		}
	}
	return cars, nil
}
