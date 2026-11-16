## 1. Scan

- [ ] 1.1 Give `PreviewRoots` and `EstimateRoots` the images-are-content flag and apply it to the walk and the preview's missing pass
- [ ] 1.2 Tests: a `-thumb` image beside a video is counted for an image-class plugin and skipped for a video-only plugin, in both the preview and the estimate

## 2. GraphQL

- [ ] 2.1 Resolve the flag from the Library's bound plugin in `scanPreview` and `scanEstimate`, and from the optional `pluginID` variable for a raw-root estimate
- [ ] 2.2 Tests: `scanPreview` and `scanEstimate` count the thumb image for an image-class plugin and skip it otherwise
