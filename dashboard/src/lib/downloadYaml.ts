import yaml from "js-yaml";
import type { Config } from "./types";

export function downloadYamlFromConfig(cfg: Config, filename: string = "instancez.yaml"): void {
  const clean = JSON.parse(JSON.stringify(cfg));
  delete (clean as any)._checksum;

  const text = yaml.dump(clean, { lineWidth: 120, noRefs: true });
  const blob = new Blob([text], { type: "application/yaml" });
  const url = URL.createObjectURL(blob);

  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
