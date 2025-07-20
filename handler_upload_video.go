package main

import (
	"bytes"

	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	//"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)


func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_streams",
		"-of", "json",
		filePath,	
	)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to run ffprobe: %w", err)
	}
	type ffprobeOutput struct {
	Streams []struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}	`json:"streams"`
}
	var output ffprobeOutput
	if err := json.Unmarshal(out.Bytes(), &output); err != nil {
		return "", fmt.Errorf("failed to parse ffprobe output: %w", err)
	}
	if len(output.Streams) == 0 {
		return "", fmt.Errorf("no video streams found in file")
	}
	width := output.Streams[0].Width
	height := output.Streams[0].Height
	if height == 0 || width == 0 {
		return "", fmt.Errorf("invalid video dimensions: %dx%d", width, height)
	}
	ratio := float64(width) / float64(height)

	if ratio > 1.7 && ratio < 1.8 {
		return "landscape", nil // ~16:9
	} else if ratio < 0.6 {
		return "portrait", nil // ~9:16
	}
	return "other", nil

}
func processVideoForFastStart(filepath string) (string, error) {
	outputPath := filepath + ".processing"
	cmd := exec.Command(
		"ffmpeg",
		"-i", filepath,
		"-c", "copy",
		"-movflags", "faststart",
		"-f", "mp4",
		outputPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg failed: %v - %s", err, stderr.String())
	}
	return outputPath, nil	
}

// func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
// 	if video.VideoURL == nil {
// 		return video, nil
// 	}
// 	parts := strings.Split(*video.VideoURL, ",")
// 	if len(parts) < 2 {
// 		return video, nil
// 	}
// 	bucket := parts[0]
// 	key := parts[1]
// 	presigned, err := generatePresignedURL(cfg.s3Client, bucket, key, 5*time.Minute)
// 	if err != nil {
// 		return video, err
// 	}
// 	video.VideoURL = &presigned
// 	return video, nil
// }

// func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
// 	presignClient := s3.NewPresignClient(s3Client)
// 	presignedUrl, err := presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
// 		Bucket: aws.String(bucket),
// 		Key:    aws.String(key),
// 	}, s3.WithPresignExpires(expireTime))
// 	if err != nil {
// 		return "", fmt.Errorf("failed to generate presigned URL: %v", err)
// 	}
// 	return presignedUrl.URL, nil
// }

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {

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
	
	const maxUploadSize = 1 << 30 //1GB
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
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
	
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form", err)
		return
	}	
	
	file, handler, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get file from form", err)
		return
	}

	defer file.Close()

	mediaType := handler.Header.Get("Content-Type")
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusUnsupportedMediaType, "Unsupported media type", nil)
		return
	}

	tmpFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create temporary file", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy file to temporary location", err)
		return
	}
	if _, err := tmpFile.Seek(0, 0); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't seek to start of temporary file", err)
		return
	}

	directory := ""
	aspectRatio, err := getVideoAspectRatio(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error determining aspect ratio", err)
		return
	}
	switch aspectRatio {
	case "16:9":
		directory = "landscape"
	case "9:16":
		directory = "portrait"
	default:
		directory = "other"
	}

	key := getAssetPath(mediaType)
	key = path.Join(directory, key)

	processedFilePath, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing video", err)
		return
	}
	defer os.Remove(processedFilePath)

	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not open processed file", err)
		return
	}
	defer processedFile.Close()

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        processedFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}
	//cloudfront URL
	url := fmt.Sprintf("https://%s/%s", cfg.s3CfDistribution, key)
	video.VideoURL = &url
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}


