package ratio_setting

import "testing"

func preserveCompletionRatioState(t *testing.T) {
	t.Helper()
	previousRatios := completionRatioMap.ReadAll()
	previousOverrideEnabled := IsCompletionRatioOverrideEnabled()
	t.Cleanup(func() {
		completionRatioMap.Clear()
		completionRatioMap.AddAll(previousRatios)
		SetCompletionRatioOverrideEnabled(previousOverrideEnabled)
	})
}

func TestCompletionRatioOverrideLockedModel(t *testing.T) {
	preserveCompletionRatioState(t)

	const modelName = "gpt-4o-2024-05-13"
	const customRatio = 9
	const hardcodedRatio = 3

	completionRatioMap.Clear()
	completionRatioMap.Set(modelName, customRatio)
	SetCompletionRatioOverrideEnabled(false)

	if got := GetCompletionRatio(modelName); got != hardcodedRatio {
		t.Fatalf("override disabled ratio = %v, want %v", got, hardcodedRatio)
	}
	info := GetCompletionRatioInfo(modelName)
	if !info.Locked || info.Ratio != hardcodedRatio {
		t.Fatalf("override disabled info = %+v, want locked hardcoded ratio %v", info, hardcodedRatio)
	}

	SetCompletionRatioOverrideEnabled(true)

	if got := GetCompletionRatio(modelName); got != customRatio {
		t.Fatalf("override enabled ratio = %v, want %v", got, customRatio)
	}
	info = GetCompletionRatioInfo(modelName)
	if info.Locked || info.Ratio != customRatio {
		t.Fatalf("override enabled info = %+v, want unlocked custom ratio %v", info, customRatio)
	}
}

func TestCompletionRatioOverrideDefaultsToEnabled(t *testing.T) {
	preserveCompletionRatioState(t)

	completionRatioOverrideEnabled.Store(true)
	completionRatioMap.Clear()
	completionRatioMap.Set("gpt-4o-2024-05-13", 9)

	if got := GetCompletionRatio("gpt-4o-2024-05-13"); got != 9 {
		t.Fatalf("default override ratio = %v, want custom ratio 9", got)
	}
}

func TestCompletionRatioOverrideUnlocksMetaWithoutCustomRatio(t *testing.T) {
	preserveCompletionRatioState(t)

	const modelName = "gpt-4o-2024-05-13"
	const hardcodedRatio = 3

	completionRatioMap.Clear()
	SetCompletionRatioOverrideEnabled(true)

	if got := GetCompletionRatio(modelName); got != hardcodedRatio {
		t.Fatalf("ratio without custom override = %v, want %v", got, hardcodedRatio)
	}
	info := GetCompletionRatioInfo(modelName)
	if info.Locked || info.Ratio != hardcodedRatio {
		t.Fatalf("info without custom override = %+v, want unlocked hardcoded ratio %v", info, hardcodedRatio)
	}
}
