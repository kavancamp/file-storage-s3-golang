package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}


	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20 // 10 MB
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form", err)
		return
	}

	file, handler, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get file from form", err)
		return
	}
	defer file.Close()

	conType := handler.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(conType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type header", err)
		return
	}	
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Content-Type header is missing", nil)
		return
	}
	if mediaType != "image/jpeg" && mediaType != "image/png"  {
		respondWithError(w, http.StatusUnsupportedMediaType, "Unsupported media type", nil)
		return
	}

	//validate
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video metadata", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not allowed to upload a thumbnail for this video", nil)
		return
	}
	//generate random filename
	var randomBytes [32]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate random bytes", err)
		return
	}
	randStr := base64.RawURLEncoding.EncodeToString(randomBytes[:])

	//Determine file extension
	var extension string
	switch mediaType {
	case "image/jpeg":
		extension = ".jpg"
	case "image/png":
		extension = ".png"
	default:
		respondWithError(w, http.StatusUnsupportedMediaType, "Unsupported media type", nil)
		return
	}		
	
	filename := randStr + extension
	savePath := filepath.Join(cfg.assetsRoot, filename)

	//save file to disk
	out, err := os.Create(savePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create file", err)
		return
	}
	defer out.Close()	

	if _, err := io.Copy(out, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't save file", err)
		return
	}	
	// update DB with new thumbnail url
	dataURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, filename)
	video.ThumbnailURL = &dataURL

	if err = cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video", err)
		return
	}
	
	respondWithJSON(w, http.StatusOK, video)

}
