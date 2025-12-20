<script lang="ts">
	import { apiClient } from '$lib/api';
	import type { SearchResponse, StatsResponse } from '$lib/types';
	import { onMount } from 'svelte';

	let query = $state('');
	let searchResponse = $state<SearchResponse | null>(null);
	let stats = $state<StatsResponse | null>(null);
	let loading = $state(false);
	let error = $state<string | null>(null);

	onMount(async () => {
		try {
			stats = await apiClient.getStats();
		} catch (err) {
			console.error('Failed to load stats:', err);
		}
	});

	async function handleSearch() {
		if (!query.trim()) return;

		loading = true;
		error = null;
		searchResponse = null;

		try {
			searchResponse = await apiClient.search(query, 3);
		} catch (err) {
			error = err instanceof Error ? err.message : 'Search failed';
		} finally {
			loading = false;
		}
	}

	function handleKeyPress(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleSearch();
		}
	}
</script>

<div class="mx-auto min-h-screen max-w-5xl p-6">
	<!-- Header -->
	<header class="mb-12 text-center">
		<h1 class="mb-3 text-5xl font-bold text-gray-900">Documentation Search</h1>
		<p class="text-lg text-gray-600">Search through documentation using AI-powered RAG</p>
		{#if stats}
			<p class="mt-2 text-sm text-gray-500">
				{stats.total_chunks.toLocaleString()} chunks indexed from {stats.total_files.toLocaleString()}
				files
			</p>
		{/if}
	</header>

	<!-- Search Box -->
	<div class="mb-8">
		<div class="relative">
			<textarea
				bind:value={query}
				onkeypress={handleKeyPress}
				placeholder="Ask a question about the documentation..."
				class="w-full resize-none rounded-lg border border-gray-300 p-4 pr-24 shadow-sm transition focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500"
				rows="3"
			></textarea>
			<button
				onclick={handleSearch}
				disabled={loading || !query.trim()}
				class="absolute bottom-3 right-3 rounded-md bg-blue-600 px-6 py-2 font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-gray-400"
			>
				{loading ? 'Searching...' : 'Search'}
			</button>
		</div>
		<p class="mt-2 text-sm text-gray-500">Press Enter to search, Shift+Enter for new line</p>
	</div>

	<!-- Error Message -->
	{#if error}
		<div class="mb-6 rounded-lg border border-red-300 bg-red-50 p-4 text-red-800">
			<strong>Error:</strong>
			{error}
		</div>
	{/if}

	<!-- Search Results -->
	{#if searchResponse}
		<div class="space-y-6">
			<!-- AI Answer -->
			{#if searchResponse.answer}
				<div class="rounded-lg border border-green-200 bg-green-50 p-6 shadow-sm">
					<h2 class="mb-3 flex items-center text-xl font-semibold text-green-900">
						<svg
							class="mr-2 h-6 w-6"
							fill="none"
							stroke="currentColor"
							viewBox="0 0 24 24"
						>
							<path
								stroke-linecap="round"
								stroke-linejoin="round"
								stroke-width="2"
								d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z"
							/>
						</svg>
						AI Answer
					</h2>
					<div class="whitespace-pre-wrap text-gray-800">{searchResponse.answer}</div>
				</div>
			{/if}

			<!-- Source Documents -->
			<div>
				<h2 class="mb-4 text-xl font-semibold text-gray-900">
					Source Documents ({searchResponse.results.length})
				</h2>
				<div class="space-y-4">
					{#each searchResponse.results as result, i}
						<div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm transition hover:shadow-md">
							<div class="mb-2 flex items-start justify-between">
								<div class="flex items-center">
									<span
										class="mr-3 flex h-8 w-8 items-center justify-center rounded-full bg-blue-100 text-sm font-semibold text-blue-800"
									>
										{i + 1}
									</span>
									<div>
										<h3 class="font-medium text-gray-900">{result.file_path}</h3>
										<p class="text-sm text-gray-500">Chunk {result.chunk_index}</p>
									</div>
								</div>
							</div>
							<p class="mt-3 text-gray-700">{result.content}</p>
						</div>
					{/each}
				</div>
			</div>
		</div>
	{:else if !loading}
		<!-- Empty State -->
		<div class="py-16 text-center">
			<svg
				class="mx-auto mb-4 h-16 w-16 text-gray-300"
				fill="none"
				stroke="currentColor"
				viewBox="0 0 24 24"
			>
				<path
					stroke-linecap="round"
					stroke-linejoin="round"
					stroke-width="2"
					d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
				/>
			</svg>
			<h3 class="mb-2 text-xl font-medium text-gray-700">Start searching</h3>
			<p class="text-gray-500">Enter a question to search through the documentation</p>
		</div>
	{/if}

	<!-- Loading State -->
	{#if loading}
		<div class="py-16 text-center">
			<div class="mx-auto mb-4 h-16 w-16 animate-spin rounded-full border-b-2 border-t-2 border-blue-600"></div>
			<p class="text-gray-600">Searching documentation...</p>
		</div>
	{/if}
</div>
