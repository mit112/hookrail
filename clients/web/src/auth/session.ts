import { request } from "../api/client";
export async function fetchSession(): Promise<boolean> {
  await request("GET", "/api/session"); return true;
}
export async function login(password: string): Promise<void> {
  await request("POST", "/api/login", { body: { password } });
}
export async function logout(): Promise<void> {
  await request("POST", "/api/logout");
}
