import { jsx as _jsx } from "react/jsx-runtime";
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { App } from "./App";
describe("App", () => {
    it("renders the shell", () => {
        render(_jsx(App, {}));
        expect(screen.getByText(/hookrail/i)).toBeInTheDocument();
    });
});
