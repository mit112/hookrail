import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { load } from "js-yaml";
import { DeliveryState } from "./schemas";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const spec = load(readFileSync(resolve(process.cwd(), "../../api/openapi.yaml"), "utf8")) as any;

describe("openapi conformance", () => {
  it("delivery state enum matches the spec", () => {
    const specEnum: string[] =
      spec.components.schemas.DeliveryListItem.properties.state.enum;
    expect([...DeliveryState.options].sort()).toEqual([...specEnum].sort());
  });
  it("deliveries documents the topic filter (not topic_pattern)", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const params = spec.paths["/v1/deliveries"].get.parameters.map((p: any) => p.name);
    expect(params).toContain("topic");
    expect(params).not.toContain("topic_pattern");
  });
});
