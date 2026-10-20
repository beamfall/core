//go:build !beamfall_appstore

package graph

func registerQBittorrentFields() {
	queryFields["qbittorrentingest"] = true
	mutationFields["setqbittorrentingest"] = true
	adminFields["qbittorrentingest"] = true
	adminFields["setqbittorrentingest"] = true
	resultTypes["qbittorrentingest"] = QBittorrentIngestInfo{}
	resultTypes["setqbittorrentingest"] = QBittorrentIngestInfo{}
}
