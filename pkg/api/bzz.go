// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethersphere/bee/pkg/log"
	"github.com/gorilla/mux"

	"github.com/ethersphere/bee/pkg/feeds"
	"github.com/ethersphere/bee/pkg/file/joiner"
	"github.com/ethersphere/bee/pkg/file/loadsave"
	"github.com/ethersphere/bee/pkg/jsonhttp"
	"github.com/ethersphere/bee/pkg/manifest"
	"github.com/ethersphere/bee/pkg/postage"
	"github.com/ethersphere/bee/pkg/sctx"
	"github.com/ethersphere/bee/pkg/storage"
	"github.com/ethersphere/bee/pkg/swarm"
	"github.com/ethersphere/bee/pkg/tags"
	"github.com/ethersphere/bee/pkg/tracing"
	"github.com/ethersphere/langos"
)

func (s *Service) bzzUploadHandler(w http.ResponseWriter, r *http.Request) {
	logger := tracing.NewLoggerWithTraceID(r.Context(), s.logger.WithName("post_bzz").Build())

	headers := struct {
		ContentType string `map:"Content-Type,mimeMediaType" validate:"required"`
	}{}
	if response := s.mapStructure(r.Header, &headers); response != nil {
		response("invalid header params", logger, w)
		return
	}

	putter, wait, err := s.newStamperPutter(r)
	if err != nil {
		logger.Debug("putter failed", "error", err)
		logger.Error(nil, "putter failed")
		switch {
		case errors.Is(err, errBatchUnusable) || errors.Is(err, postage.ErrNotUsable):
			jsonhttp.UnprocessableEntity(w, "batch not usable yet or does not exist")
		case errors.Is(err, postage.ErrNotFound):
			jsonhttp.NotFound(w, "batch with id not found")
		case errors.Is(err, errInvalidPostageBatch):
			jsonhttp.BadRequest(w, "invalid batch id")
		case errors.Is(err, errUnsupportedDevNodeOperation):
			jsonhttp.BadRequest(w, errUnsupportedDevNodeOperation)
		default:
			jsonhttp.BadRequest(w, nil)
		}
		return
	}

	isDir := r.Header.Get(SwarmCollectionHeader)
	if strings.ToLower(isDir) == "true" || headers.ContentType == multiPartFormData {
		s.dirUploadHandler(logger, w, r, putter, wait)
		return
	}
	s.fileUploadHandler(logger, w, r, putter, wait)
}

// fileUploadResponse is returned when an HTTP request to upload a file is successful
type bzzUploadResponse struct {
	Reference swarm.Address `json:"reference"`
}

// fileUploadHandler uploads the file and its metadata supplied in the file body and
// the headers
func (s *Service) fileUploadHandler(logger log.Logger, w http.ResponseWriter, r *http.Request, storer storage.Storer, waitFn func() error) {
	queries := struct {
		FileName string `map:"name" validate:"startsnotwith=/"`
	}{}
	if response := s.mapStructure(r.URL.Query(), &queries); response != nil {
		response("invalid query params", logger, w)
		return
	}

	tag, created, err := s.getOrCreateTag(r.Header.Get(SwarmTagHeader))
	if err != nil {
		logger.Debug("get or create tag failed", "error", err)
		logger.Error(nil, "get or create tag failed")
		switch {
		case errors.Is(err, tags.ErrNotFound):
			jsonhttp.NotFound(w, "tag not found")
		default:
			jsonhttp.InternalServerError(w, "cannot get or create tag")
		}
		return
	}

	if !created {
		// only in the case when tag is sent via header (i.e. not created by this request)
		if estimatedTotalChunks := requestCalculateNumberOfChunks(r); estimatedTotalChunks > 0 {
			err = tag.IncN(tags.TotalChunks, estimatedTotalChunks)
			if err != nil {
				logger.Debug("increment tag failed", "error", err)
				logger.Error(nil, "increment tag failed")
				jsonhttp.InternalServerError(w, "increment tag failed")
				return
			}
		}
	}

	// Add the tag to the context
	ctx := sctx.SetTag(r.Context(), tag)
	p := requestPipelineFn(storer, r)

	// first store the file and get its reference
	fr, err := p(ctx, r.Body)
	if err != nil {
		logger.Debug("file store failed", "file_name", queries.FileName, "error", err)
		logger.Error(nil, "file store failed", "file_name", queries.FileName)
		switch {
		case errors.Is(err, postage.ErrBucketFull):
			jsonhttp.PaymentRequired(w, "batch is overissued")
		default:
			jsonhttp.InternalServerError(w, errFileStore)
		}
		return
	}

	// If filename is still empty, use the file hash as the filename
	if queries.FileName == "" {
		queries.FileName = fr.String()
		if err := s.validate.Struct(queries); err != nil {
			verr := &validationError{
				Entry: "file hash",
				Value: queries.FileName,
				Cause: err,
			}
			logger.Debug("invalid body filename", "error", verr)
			logger.Error(nil, "invalid body filename")
			jsonhttp.BadRequest(w, jsonhttp.StatusResponse{
				Message: "invalid body params",
				Code:    http.StatusBadRequest,
				Reasons: []jsonhttp.Reason{{
					Field: "file hash",
					Error: verr.Error(),
				}},
			})
			return
		}
	}

	encrypt := requestEncrypt(r)
	factory := requestPipelineFactory(ctx, storer, r)
	l := loadsave.New(storer, factory)

	m, err := manifest.NewDefaultManifest(l, encrypt)
	if err != nil {
		logger.Debug("create manifest failed", "file_name", queries.FileName, "error", err)
		logger.Error(nil, "create manifest failed", "file_name", queries.FileName)
		switch {
		case errors.Is(err, manifest.ErrInvalidManifestType):
			jsonhttp.BadRequest(w, "create manifest failed")
		default:
			jsonhttp.InternalServerError(w, nil)
		}
		return
	}

	rootMetadata := map[string]string{
		manifest.WebsiteIndexDocumentSuffixKey: queries.FileName,
	}
	err = m.Add(ctx, manifest.RootPath, manifest.NewEntry(swarm.ZeroAddress, rootMetadata))
	if err != nil {
		logger.Debug("adding metadata to manifest failed", "file_name", queries.FileName, "error", err)
		logger.Error(nil, "adding metadata to manifest failed", "file_name", queries.FileName)
		jsonhttp.InternalServerError(w, "add metadata failed")
		return
	}

	fileMtdt := map[string]string{
		manifest.EntryMetadataContentTypeKey: r.Header.Get(contentTypeHeader), // Content-Type has already been validated.
		manifest.EntryMetadataFilenameKey:    queries.FileName,
	}

	err = m.Add(ctx, queries.FileName, manifest.NewEntry(fr, fileMtdt))
	if err != nil {
		logger.Debug("adding file to manifest failed", "file_name", queries.FileName, "error", err)
		logger.Error(nil, "adding file to manifest failed", "file_name", queries.FileName)
		jsonhttp.InternalServerError(w, "add file failed")
		return
	}

	logger.Debug("info", "encrypt", encrypt, "file_name", queries.FileName, "hash", fr, "metadata", fileMtdt)

	storeSizeFn := []manifest.StoreSizeFunc{}
	if !created {
		// only in the case when tag is sent via header (i.e. not created by this request)
		// each content that is saved for manifest
		storeSizeFn = append(storeSizeFn, func(dataSize int64) error {
			if estimatedTotalChunks := calculateNumberOfChunks(dataSize, encrypt); estimatedTotalChunks > 0 {
				err = tag.IncN(tags.TotalChunks, estimatedTotalChunks)
				if err != nil {
					return fmt.Errorf("increment tag: %w", err)
				}
			}
			return nil
		})
	}

	manifestReference, err := m.Store(ctx, storeSizeFn...)
	if err != nil {
		logger.Debug("manifest store failed", "file_name", queries.FileName, "error", err)
		logger.Error(nil, "manifest store failed", "file_name", queries.FileName)
		switch {
		case errors.Is(err, postage.ErrBucketFull):
			jsonhttp.PaymentRequired(w, "batch is overissued")
		default:
			jsonhttp.InternalServerError(w, "manifest store failed")
		}
		return
	}
	logger.Debug("store", "manifest_reference", manifestReference)

	if created {
		_, err = tag.DoneSplit(manifestReference)
		if err != nil {
			logger.Debug("done split failed", "error", err)
			logger.Error(nil, "done split failed")
			jsonhttp.InternalServerError(w, "done split failed")
			return
		}
	}

	if requestPin(r) {
		if err := s.pinning.CreatePin(ctx, manifestReference, false); err != nil {
			logger.Debug("pin creation failed", "manifest_reference", manifestReference, "error", err)
			logger.Error(nil, "pin creation failed")
			jsonhttp.InternalServerError(w, "create pin failed")
			return
		}
	}

	if err = waitFn(); err != nil {
		logger.Debug("sync chunks failed", "error", err)
		logger.Error(nil, "sync chunks failed")
		jsonhttp.InternalServerError(w, "sync chunks failed")
		return
	}

	w.Header().Set("ETag", fmt.Sprintf("%q", manifestReference.String()))
	w.Header().Set(SwarmTagHeader, fmt.Sprint(tag.Uid))
	w.Header().Set("Access-Control-Expose-Headers", SwarmTagHeader)
	jsonhttp.Created(w, bzzUploadResponse{
		Reference: manifestReference,
	})
}

func (s *Service) bzzDownloadHandler(w http.ResponseWriter, r *http.Request) {
	logger := tracing.NewLoggerWithTraceID(r.Context(), s.logger.WithName("get_bzz_by_path").Build())

	paths := struct {
		Address swarm.Address `map:"address,resolve" validate:"required"`
		Path    string        `map:"path"`
	}{}
	if response := s.mapStructure(mux.Vars(r), &paths); response != nil {
		response("invalid path params", logger, w)
		return
	}

	if strings.HasSuffix(paths.Path, "/") {
		paths.Path = strings.TrimRight(paths.Path, "/") + "/" // NOTE: leave one slash if there was some.
	}

	s.serveReference(logger, paths.Address, paths.Path, w, r)
}

func (s *Service) serveReference(logger log.Logger, address swarm.Address, pathVar string, w http.ResponseWriter, r *http.Request) {
	loggerV1 := logger.V(1).Build()

	ls := loadsave.NewReadonly(s.storer)
	feedDereferenced := false

	ctx := r.Context()

FETCH:
	// read manifest entry
	m, err := manifest.NewDefaultManifestReference(
		address,
		ls,
	)
	if err != nil {
		logger.Debug("bzz download: not manifest", "address", address, "error", err)
		logger.Error(nil, "not manifest")
		jsonhttp.NotFound(w, nil)
		return
	}

	// there's a possible ambiguity here, right now the data which was
	// read can be an entry.Entry or a mantaray feed manifest. Try to
	// unmarshal as mantaray first and possibly resolve the feed, otherwise
	// go on normally.
	if !feedDereferenced {
		if l, err := s.manifestFeed(ctx, m); err == nil {
			//we have a feed manifest here
			ch, cur, _, err := l.At(ctx, time.Now().Unix(), 0)
			if err != nil {
				logger.Debug("bzz download: feed lookup failed", "error", err)
				logger.Error(nil, "bzz download: feed lookup failed")
				jsonhttp.NotFound(w, "feed not found")
				return
			}
			if ch == nil {
				logger.Debug("bzz download: feed lookup: no updates")
				logger.Error(nil, "bzz download: feed lookup")
				jsonhttp.NotFound(w, "no update found")
				return
			}
			ref, _, err := parseFeedUpdate(ch)
			if err != nil {
				logger.Debug("bzz download: mapStructure feed update failed", "error", err)
				logger.Error(nil, "bzz download: mapStructure feed update failed")
				jsonhttp.InternalServerError(w, "mapStructure feed update")
				return
			}
			address = ref
			feedDereferenced = true
			curBytes, err := cur.MarshalBinary()
			if err != nil {
				s.logger.Debug("bzz download: marshal feed index failed", "error", err)
				s.logger.Error(nil, "bzz download: marshal index failed")
				jsonhttp.InternalServerError(w, "marshal index")
				return
			}

			w.Header().Set(SwarmFeedIndexHeader, hex.EncodeToString(curBytes))
			// this header might be overriding others. handle with care. in the future
			// we should implement an append functionality for this specific header,
			// since different parts of handlers might be overriding others' values
			// resulting in inconsistent headers in the response.
			w.Header().Set("Access-Control-Expose-Headers", SwarmFeedIndexHeader)
			goto FETCH
		}
	}

	if pathVar == "" {
		loggerV1.Debug("bzz download: handle empty path", "address", address)

		if indexDocumentSuffixKey, ok := manifestMetadataLoad(ctx, m, manifest.RootPath, manifest.WebsiteIndexDocumentSuffixKey); ok {
			pathWithIndex := path.Join(pathVar, indexDocumentSuffixKey)
			indexDocumentManifestEntry, err := m.Lookup(ctx, pathWithIndex)
			if err == nil {
				// index document exists
				logger.Debug("bzz download: serving path", "path", pathWithIndex)

				s.serveManifestEntry(logger, w, r, indexDocumentManifestEntry, !feedDereferenced)
				return
			}
		}
		logger.Debug("bzz download: address not found or incorrect", "address", address, "path", pathVar)
		logger.Error(nil, "address not found or incorrect")
		jsonhttp.NotFound(w, "address not found or incorrect")
		return
	}

	me, err := m.Lookup(ctx, pathVar)
	if err != nil {
		loggerV1.Debug("bzz download: invalid path", "address", address, "path", pathVar, "error", err)
		logger.Error(nil, "bzz download: invalid path")

		if errors.Is(err, manifest.ErrNotFound) {

			if !strings.HasPrefix(pathVar, "/") {
				// check for directory
				dirPath := pathVar + "/"
				exists, err := m.HasPrefix(ctx, dirPath)
				if err == nil && exists {
					// redirect to directory
					u := r.URL
					u.Path += "/"
					redirectURL := u.String()

					logger.Debug("bzz download: redirecting failed", "url", redirectURL, "error", err)

					http.Redirect(w, r, redirectURL, http.StatusPermanentRedirect)
					return
				}
			}

			// check index suffix path
			if indexDocumentSuffixKey, ok := manifestMetadataLoad(ctx, m, manifest.RootPath, manifest.WebsiteIndexDocumentSuffixKey); ok {
				if !strings.HasSuffix(pathVar, indexDocumentSuffixKey) {
					// check if path is directory with index
					pathWithIndex := path.Join(pathVar, indexDocumentSuffixKey)
					indexDocumentManifestEntry, err := m.Lookup(ctx, pathWithIndex)
					if err == nil {
						// index document exists
						logger.Debug("bzz download: serving path", "path", pathWithIndex)

						s.serveManifestEntry(logger, w, r, indexDocumentManifestEntry, !feedDereferenced)
						return
					}
				}
			}

			// check if error document is to be shown
			if errorDocumentPath, ok := manifestMetadataLoad(ctx, m, manifest.RootPath, manifest.WebsiteErrorDocumentPathKey); ok {
				if pathVar != errorDocumentPath {
					errorDocumentManifestEntry, err := m.Lookup(ctx, errorDocumentPath)
					if err == nil {
						// error document exists
						logger.Debug("bzz download: serving path", "path", errorDocumentPath)

						s.serveManifestEntry(logger, w, r, errorDocumentManifestEntry, !feedDereferenced)
						return
					}
				}
			}

			jsonhttp.NotFound(w, "path address not found")
		} else {
			jsonhttp.NotFound(w, nil)
		}
		return
	}

	// serve requested path
	s.serveManifestEntry(logger, w, r, me, !feedDereferenced)
}

func (s *Service) serveManifestEntry(
	logger log.Logger,
	w http.ResponseWriter,
	r *http.Request,
	manifestEntry manifest.Entry,
	etag bool,
) {
	additionalHeaders := http.Header{}
	mtdt := manifestEntry.Metadata()
	if fname, ok := mtdt[manifest.EntryMetadataFilenameKey]; ok {
		fname = filepath.Base(fname) // only keep the file name
		additionalHeaders["Content-Disposition"] =
			[]string{fmt.Sprintf("inline; filename=\"%s\"", fname)}
	}
	if mimeType, ok := mtdt[manifest.EntryMetadataContentTypeKey]; ok {
		additionalHeaders["Content-Type"] = []string{mimeType}
	}

	s.downloadHandler(logger, w, r, manifestEntry.Reference(), additionalHeaders, etag)
}

// downloadHandler contains common logic for dowloading Swarm file from API
func (s *Service) downloadHandler(logger log.Logger, w http.ResponseWriter, r *http.Request, reference swarm.Address, additionalHeaders http.Header, etag bool) {
	reader, l, err := joiner.New(r.Context(), s.storer, reference)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			logger.Debug("api download: not found ", "address", reference, "error", err)
			logger.Error(nil, "not found")
			jsonhttp.NotFound(w, nil)
			return
		}
		logger.Debug("api download: unexpected error", "address", reference, "error", err)
		logger.Error(nil, "api download: unexpected error")
		jsonhttp.InternalServerError(w, "joiner failed")
		return
	}

	// include additional headers
	for name, values := range additionalHeaders {
		w.Header().Set(name, strings.Join(values, "; "))
	}
	if etag {
		w.Header().Set("ETag", fmt.Sprintf("%q", reference))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(l, 10))
	w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
	http.ServeContent(w, r, "", time.Now(), langos.NewBufferedLangos(reader, lookaheadBufferSize(l)))
}

// manifestMetadataLoad returns the value for a key stored in the metadata of
// manifest path, or empty string if no value is present.
// The ok result indicates whether value was found in the metadata.
func manifestMetadataLoad(
	ctx context.Context,
	manifest manifest.Interface,
	path, metadataKey string,
) (string, bool) {
	me, err := manifest.Lookup(ctx, path)
	if err != nil {
		return "", false
	}

	manifestRootMetadata := me.Metadata()
	if val, ok := manifestRootMetadata[metadataKey]; ok {
		return val, ok
	}

	return "", false
}

func (s *Service) manifestFeed(
	ctx context.Context,
	m manifest.Interface,
) (feeds.Lookup, error) {
	e, err := m.Lookup(ctx, "/")
	if err != nil {
		return nil, fmt.Errorf("node lookup: %w", err)
	}
	var (
		owner, topic []byte
		t            = new(feeds.Type)
	)
	meta := e.Metadata()
	if e := meta[feedMetadataEntryOwner]; e != "" {
		owner, err = hex.DecodeString(e)
		if err != nil {
			return nil, err
		}
	}
	if e := meta[feedMetadataEntryTopic]; e != "" {
		topic, err = hex.DecodeString(e)
		if err != nil {
			return nil, err
		}
	}
	if e := meta[feedMetadataEntryType]; e != "" {
		err := t.FromString(e)
		if err != nil {
			return nil, err
		}
	}
	if len(owner) == 0 || len(topic) == 0 {
		return nil, fmt.Errorf("node lookup: %s", "feed metadata absent")
	}
	f := feeds.New(topic, common.BytesToAddress(owner))
	return s.feedFactory.NewLookup(*t, f)
}
