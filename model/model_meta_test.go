package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAllModelTags(t *testing.T) {
	truncateTables(t)

	require.NoError(t, DB.Create(&Model{ModelName: "model-a", Tags: "fast, stable,chat"}).Error)
	require.NoError(t, DB.Create(&Model{ModelName: "model-b", Tags: "stable, vision,fast"}).Error)
	require.NoError(t, DB.Create(&Model{ModelName: "model-c", Tags: " , ,"}).Error)
	require.NoError(t, DB.Create(&Model{ModelName: "model-d"}).Error)

	tags, err := GetAllModelTags()
	require.NoError(t, err)
	require.Equal(t, []string{"chat", "fast", "stable", "vision"}, tags)
}
