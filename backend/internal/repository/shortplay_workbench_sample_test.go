package repository

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

// W02 首个回放：把 testdata/shortplay-workbench-port/sample/two-episodes.json 写进
// 真实仓储再读回，验证「两集共用一个角色、第二集是它的衍生造型」在目标模型上成立。
// 样本本身是供体固定提交的源码观察产物，回放通过后它才从 source-observed 升级为 runtime。

type shortplaySample struct {
	Project struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		Name        string `json:"name"`
		AspectRatio string `json:"aspectRatio"`
	} `json:"project"`
	Shortplay struct {
		ID           string `json:"id"`
		SettingsJSON string `json:"settingsJson"`
	} `json:"shortplay"`
	Episodes []struct {
		ID       string `json:"id"`
		Number   int    `json:"number"`
		Title    string `json:"title"`
		CanvasID string `json:"canvasId"`
		AssetIDs []int  `json:"assetIds"`
	} `json:"episodes"`
	Assets []struct {
		ID             int      `json:"id"`
		Kind           string   `json:"kind"`
		Name           string   `json:"name"`
		Aliases        []string `json:"aliases"`
		Description    string   `json:"description"`
		Appearance     string   `json:"appearance"`
		Voice          string   `json:"voice"`
		Locked         bool     `json:"locked"`
		Revision       int64    `json:"revision"`
		ImageStatus    string   `json:"imageStatus"`
		ImageVersion   int      `json:"imageVersion"`
		EpisodeNumbers []int    `json:"episodeNumbers"`
		Position       int      `json:"position"`
		DerivedFrom    int      `json:"derivedFrom"`
		VariantName    string   `json:"variantName"`
		RenderRule     string   `json:"renderRule"`
		CurrentImageID int      `json:"currentImageId"`
		CurrentAudioID int      `json:"currentAudioId"`
		AudioStatus    string   `json:"audioStatus"`
		AudioVersion   int      `json:"audioVersion"`
	} `json:"assets"`
}

func loadShortplaySample(t *testing.T) shortplaySample {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "shortplay-workbench-port", "sample", "two-episodes.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	var sample shortplaySample
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	return sample
}

func TestShortplayWorkbenchSampleReplay(t *testing.T) {
	repo, _ := newShortplayTestRepository(t)
	sample := loadShortplaySample(t)
	now := time.Now()

	project := &model.Project{
		ID: sample.Project.ID, UserID: "user-w01", Name: sample.Project.Name, Type: sample.Project.Type,
		AspectRatio: sample.Project.AspectRatio, Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	shortplay := &model.Shortplay{
		ID: sample.Shortplay.ID, ProjectID: project.ID, UserID: project.UserID,
		SettingsJSON: sample.Shortplay.SettingsJSON, CreatedAt: now, UpdatedAt: now,
	}
	episodes := make([]model.ShortplayEpisode, 0, len(sample.Episodes))
	for _, episode := range sample.Episodes {
		assetIDs, err := json.Marshal(episode.AssetIDs)
		if err != nil {
			t.Fatal(err)
		}
		episodes = append(episodes, model.ShortplayEpisode{
			ID: episode.ID, ShortplayID: shortplay.ID, Number: episode.Number, Title: episode.Title,
			CanvasID: episode.CanvasID, AssetIDsJSON: string(assetIDs), CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := repo.CreateShortplayProject(project, shortplay, episodes); err != nil {
		t.Fatalf("create sample project: %v", err)
	}
	for _, asset := range sample.Assets {
		aliases, err := json.Marshal(asset.Aliases)
		if err != nil {
			t.Fatal(err)
		}
		numbers, err := json.Marshal(asset.EpisodeNumbers)
		if err != nil {
			t.Fatal(err)
		}
		row := model.ShortplayAsset{
			ShortplayID: shortplay.ID, ID: asset.ID, Kind: asset.Kind, Name: asset.Name,
			AliasesJSON: string(aliases), Description: asset.Description, Appearance: asset.Appearance,
			Voice: asset.Voice, Locked: asset.Locked, Revision: asset.Revision,
			ImageStatus: asset.ImageStatus, ImageVersion: asset.ImageVersion,
			EpisodeNumbersJSON: string(numbers), Position: asset.Position,
			DerivedFrom: asset.DerivedFrom, VariantName: asset.VariantName, RenderRule: asset.RenderRule,
			CurrentImageID: asset.CurrentImageID, CurrentAudioID: asset.CurrentAudioID,
			AudioStatus: asset.AudioStatus, AudioVersion: asset.AudioVersion,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := repo.CreateShortplayAsset(&row); err != nil {
			t.Fatalf("create sample asset %d: %v", asset.ID, err)
		}
	}

	// 读回：两行资产，第二行是基础角色的衍生造型（名字与音色由读取层合成/回落）。
	assets, err := repo.ShortplayAssets(shortplay.ID)
	if err != nil {
		t.Fatalf("read sample assets: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("assets = %d, want 2", len(assets))
	}
	if assets[0].ID != 1 || assets[0].Name != "沈青禾" || assets[0].DerivedFrom != 0 {
		t.Fatalf("base asset = %+v, want id 1 基础角色", assets[0])
	}
	if assets[1].ID != 2 || assets[1].DerivedFrom != 1 || assets[1].VariantName != "战袍" {
		t.Fatalf("derived asset = %+v, want id 2 derived from 1 with 战袍", assets[1])
	}
	if assets[1].Name != "" {
		t.Fatalf("derived row stores name = %q; the read model synthesises it", assets[1].Name)
	}

	// 集绑定：第一集只有共享角色，第二集同时引用角色与衍生造型。
	storedEpisodes, err := repo.ShortplayEpisodes(shortplay.ID)
	if err != nil {
		t.Fatalf("read sample episodes: %v", err)
	}
	if len(storedEpisodes) != 2 {
		t.Fatalf("episodes = %d, want 2", len(storedEpisodes))
	}
	var episodeOneIDs, episodeTwoIDs []int
	if err := json.Unmarshal([]byte(storedEpisodes[0].AssetIDsJSON), &episodeOneIDs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(storedEpisodes[1].AssetIDsJSON), &episodeTwoIDs); err != nil {
		t.Fatal(err)
	}
	if len(episodeOneIDs) != 1 || episodeOneIDs[0] != 1 {
		t.Fatalf("episode 1 asset ids = %v, want [1]", episodeOneIDs)
	}
	sort.Ints(episodeTwoIDs)
	if len(episodeTwoIDs) != 2 || episodeTwoIDs[0] != 1 || episodeTwoIDs[1] != 2 {
		t.Fatalf("episode 2 asset ids = %v, want [1 2]", episodeTwoIDs)
	}

	// 同一账号的第二个作品可以从资产 1 重新发号，且读不到本作品的资产。
	other := &model.Project{
		ID: "proj-w01-other", UserID: project.UserID, Name: "另一个作品", Type: "shortplay",
		AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	otherShortplay := &model.Shortplay{ID: "sp-w01-other", ProjectID: other.ID, UserID: project.UserID, SettingsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := repo.CreateShortplayProject(other, otherShortplay, nil); err != nil {
		t.Fatalf("create second project: %v", err)
	}
	next, err := repo.NextShortplayAssetID(otherShortplay.ID)
	if err != nil || next != 1 {
		t.Fatalf("second project next asset id = %d, %v; want 1", next, err)
	}
	if _, err := repo.ShortplayAssetByID(otherShortplay.ID, 1); !errors.Is(err, ErrShortplayAssetNotFound) {
		t.Fatalf("cross-project asset read err = %v, want ErrShortplayAssetNotFound", err)
	}
	if next, err := repo.NextShortplayAssetID(shortplay.ID); err != nil || next != 3 {
		t.Fatalf("sample project next asset id = %d, %v; want 3", next, err)
	}
}
