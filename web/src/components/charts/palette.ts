import { useResolvedTheme } from "../../lib/theme";

// Chart colors, validated with the dataviz palette validator for both surfaces:
// succeeded/failed are lightness-separated so they stay distinct under red-green
// colour blindness (deutan ΔE 17.8 light, protan ΔE 8.6 dark). Cost uses the accent blue.
const LIGHT = {
  succeeded: "#5cb85c",
  failed: "#b52f2f",
  cost: "#2a78d6",
  grid: "#e1e0d9",
  axis: "#c3c2b7",
  tick: "#7a7873",
  surface: "#ffffff",
  hover: "rgba(11,11,11,0.05)",
};

const DARK: typeof LIGHT = {
  succeeded: "#1f7f1f",
  failed: "#e66767",
  cost: "#3987e5",
  grid: "#2c2c2a",
  axis: "#383835",
  tick: "#8f8d86",
  surface: "#161615",
  hover: "rgba(255,255,255,0.06)",
};

export type ChartPalette = typeof LIGHT;

export function useChartPalette(): ChartPalette {
  return useResolvedTheme() === "dark" ? DARK : LIGHT;
}
