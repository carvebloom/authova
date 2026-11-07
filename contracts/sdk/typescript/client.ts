// Copyright Authova contributors. SPDX-License-Identifier: Apache-2.0
// Thin fetch clients over the Proto-derived public contracts. No write retries.
import type { paths as ManagementPaths } from '../../generated/typescript/management';
import type { paths as QueryPaths } from '../../generated/typescript/query';

type Method = 'get' | 'post' | 'put' | 'patch' | 'delete';
type Operation<P, K extends keyof P, M extends Method> = M extends keyof P[K]
  ? NonNullable<P[K][M]>
  : never;
type Body<O> = O extends { requestBody: { content: { 'application/json': infer B } } }
  ? { body: B }
  : { body?: never };
type Input<O> = (O extends { parameters: infer P } ? P : object) & Body<O>;
type Content<R> = R extends { content: { 'application/json': infer C } }
  ? C
  : R extends { content: { 'application/problem+json': infer C } }
    ? C
    : undefined;
type Response<O> = O extends { responses: infer R }
  ? { [S in keyof R]: { status: S; data: Content<R[S]>; headers: Headers } }[keyof R]
  : never;

export interface ClientOptions {
  baseURL: string;
  token: () => string | Promise<string>;
  fetch?: typeof fetch;
}
export function createManagementClient(options: ClientOptions) {
  return createClient<ManagementPaths>(options);
}
export function createQueryClient(options: ClientOptions) {
  return createClient<QueryPaths>(options);
}
function createClient<P>(options: ClientOptions) {
  const base = new URL(options.baseURL);
  const send = options.fetch ?? fetch;
  return {
    async request<K extends keyof P & string, M extends Method & keyof P[K]>(
      path: K,
      method: M,
      input: Input<Operation<P, K, M>>,
      signal?: AbortSignal,
    ): Promise<Response<Operation<P, K, M>>> {
      const values = input as {
        path?: Record<string, string>;
        query?: Record<string, string | number | boolean | undefined>;
        header?: Record<string, string | undefined>;
        body?: unknown;
      };
      const route = path.replace(/\{([^}]+)\}/g, (_, name: string) => {
        const value = values.path?.[name];
        if (value === undefined) throw new Error(`Missing path parameter: ${name}`);
        return encodeURIComponent(value);
      });
      const url = new URL(route, base);
      for (const [name, value] of Object.entries(values.query ?? {})) {
        if (value !== undefined) url.searchParams.set(name, String(value));
      }
      const headers = new Headers();
      for (const [name, value] of Object.entries(values.header ?? {})) {
        if (value !== undefined) headers.set(name, value);
      }
      headers.set('Authorization', `Bearer ${await options.token()}`);
      headers.set('Accept', 'application/json');
      if (values.body !== undefined) headers.set('Content-Type', 'application/json');
      const result = await send(url, {
        method: method.toUpperCase(),
        headers,
        body: values.body === undefined ? undefined : JSON.stringify(values.body),
        signal,
        credentials: 'omit',
        redirect: 'error',
      });
      const text = await result.text();
      return {
        status: result.status,
        data: text === '' ? undefined : JSON.parse(text),
        headers: result.headers,
      } as Response<Operation<P, K, M>>;
    },
  };
}
