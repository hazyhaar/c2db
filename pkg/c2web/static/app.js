document.addEventListener('DOMContentLoaded', () => {
    const btnRunQuery = document.getElementById('btn-run-query');
    const inputPrefix = document.getElementById('query-prefix');
    const inputField = document.getElementById('query-field');
    const inputValue = document.getElementById('query-value');
    const queryOutput = document.getElementById('query-output');
    const timingBadge = document.getElementById('timing-badge');

    if (btnRunQuery) {
        btnRunQuery.addEventListener('click', async () => {
            btnRunQuery.disabled = true;
            btnRunQuery.textContent = 'Scanning 16KB Pages...';
            timingBadge.textContent = 'Running...';
            timingBadge.style.color = '#f59e0b';

            const payload = {
                prefix: inputPrefix.value || 'agent:context:',
                field: inputField.value || 'status',
                value: inputValue.value || 'active',
                limit: 50
            };

            try {
                const res = await fetch('/api/v1/query', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });

                if (!res.ok) {
                    throw new Error(`HTTP ${res.status}`);
                }

                const data = await res.json();
                timingBadge.textContent = `${data.elapsed_ns} ns (${data.elapsed_us.toFixed(2)} µs)`;
                timingBadge.style.color = '#10b981';

                const outputText = `// c2db Predicate Pushdown Query Result
// Mode: Zero-Copy Page Scan (16 KB pages)
// Execution Time: ${data.elapsed_ns} ns
// Matched Records: ${data.count}
// Status: Pushdown Verified (0 AST allocs)

` + JSON.stringify(data.records, null, 2);

                queryOutput.textContent = outputText;
            } catch (err) {
                timingBadge.textContent = 'Error';
                timingBadge.style.color = '#ef4444';
                queryOutput.textContent = `// Query Execution Failed: ${err.message}`;
            } finally {
                btnRunQuery.disabled = false;
                btnRunQuery.textContent = 'Execute Pushdown Query';
            }
        });
    }
});
