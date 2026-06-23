import { request } from "../api/client";
import type { Role } from "./role";

export interface Session {
  authenticated: boolean;
  role: Role;
  username: string;
}

export async function fetchSession(): Promise<Session> {
  return request<Session>("GET", "/api/session");
}

export async function login(username: string, password: string): Promise<void> {
  await request("POST", "/api/login", { body: { username, password } });
}

export async function logout(): Promise<void> {
  await request("POST", "/api/logout");
}
