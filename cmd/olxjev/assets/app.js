// OlxJev web UI: the form works without this script (GET /?q=); with it the
// result card is fetched from /result and swapped in, and recent queries are
// kept in this browser.
(function () {
    "use strict";

    var form = document.querySelector(".ask");
    var input = document.getElementById("q");
    var result = document.getElementById("result");
    var historyBox = document.getElementById("history");
    var historyList = document.getElementById("history-list");
    var HISTORY_KEY = "olxjev.history";
    var HISTORY_MAX = 10;
    var cache = {}; // backend + query -> result fragment, for back/forward
    var inflight = null;
    var radios = document.querySelectorAll('input[name="backend"]'); // none with a single backend

    // the switch's backend; "" without a switch (the server's default answers)
    function backend() {
        var checked = document.querySelector('input[name="backend"]:checked');
        return checked ? checked.value : "";
    }

    function setBackend(name) {
        radios.forEach(function (r) {
            if (name && r.value === name) {
                r.checked = true;
            }
        });
    }

    function params(q) {
        var p = "q=" + encodeURIComponent(q);
        if (backend()) {
            p += "&backend=" + encodeURIComponent(backend());
        }
        return p;
    }

    function key(q, b) {
        return b + "\n" + q;
    }

    function autosize() {
        input.style.height = "auto";
        input.style.height = Math.min(input.scrollHeight, 220) + "px";
    }

    function skeleton() {
        return '<article class="card"><div class="skeleton"><i></i><i></i><i></i></div></article>';
    }

    function show(html) {
        result.innerHTML = html;
        remember(result.querySelector(".card"));
    }

    function ask(q, push) {
        q = q.trim();
        if (!q) {
            input.focus();
            return;
        }
        if (inflight) {
            inflight.abort();
        }
        var ctrl = new AbortController();
        var b = backend();
        inflight = ctrl;
        form.classList.add("busy");
        result.innerHTML = skeleton();
        if (push) {
            history.pushState({ q: q, backend: b }, "", "/?" + params(q));
        }
        document.title = q + " · OlxJev";
        fetch("/result?" + params(q), { signal: ctrl.signal })
            .then(function (r) { return r.text(); })
            .then(function (html) {
                cache[key(q, b)] = html;
                show(html);
            })
            .catch(function (err) {
                if (err.name !== "AbortError") {
                    result.innerHTML = '<article class="card"><p class="error"></p></article>';
                    result.querySelector(".error").textContent = "Не вдалося зв'язатися із сервером: " + err.message;
                }
            })
            .finally(function () {
                if (inflight === ctrl) {
                    inflight = null;
                    form.classList.remove("busy");
                }
            });
    }

    // history in localStorage — a per-browser convenience, may be unavailable
    function loadHistory() {
        try {
            var items = JSON.parse(localStorage.getItem(HISTORY_KEY) || "[]");
            return Array.isArray(items) ? items : [];
        } catch (e) {
            return [];
        }
    }

    function saveHistory(items) {
        try {
            localStorage.setItem(HISTORY_KEY, JSON.stringify(items));
        } catch (e) { /* storage blocked */ }
    }

    function remember(card) {
        if (!card || !card.dataset.query) {
            return;
        }
        var q = card.dataset.query;
        var b = radios.length ? card.dataset.backend || "" : "";
        var items = loadHistory().filter(function (it) { return it.q !== q || (it.backend || "") !== b; });
        items.unshift({ q: q, url: card.dataset.url || "", backend: b });
        saveHistory(items.slice(0, HISTORY_MAX));
        renderHistory();
    }

    function renderHistory() {
        var items = loadHistory();
        historyList.textContent = "";
        historyBox.hidden = items.length === 0;
        items.forEach(function (it) {
            var li = document.createElement("li");
            var again = document.createElement("button");
            again.type = "button";
            again.className = "again";
            again.textContent = it.q;
            again.title = "Повторити запит";
            again.addEventListener("click", function () {
                input.value = it.q;
                autosize();
                setBackend(it.backend);
                ask(it.q, true);
            });
            li.appendChild(again);
            if (radios.length && it.backend) {
                var tag = document.createElement("span");
                tag.className = "tag";
                tag.textContent = it.backend;
                li.appendChild(tag);
            }
            if (it.url) {
                var open = document.createElement("a");
                open.className = "open";
                open.href = it.url;
                open.target = "_blank";
                open.rel = "noopener";
                open.textContent = "OLX ↗";
                li.appendChild(open);
            }
            historyList.appendChild(li);
        });
    }

    function copy(text, btn) {
        var done = function () {
            var label = btn.textContent;
            btn.textContent = "Скопійовано ✓";
            btn.classList.add("done");
            setTimeout(function () {
                btn.textContent = label;
                btn.classList.remove("done");
            }, 1400);
        };
        if (navigator.clipboard && window.isSecureContext) {
            navigator.clipboard.writeText(text).then(done);
            return;
        }
        var ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        try {
            document.execCommand("copy");
            done();
        } catch (e) { /* nothing to do */ }
        ta.remove();
    }

    form.addEventListener("submit", function (e) {
        e.preventDefault();
        ask(input.value, true);
    });

    input.addEventListener("keydown", function (e) {
        if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
            e.preventDefault();
            ask(input.value, true);
        }
    });
    input.addEventListener("input", autosize);

    document.addEventListener("click", function (e) {
        var ex = e.target.closest(".example, .suggest");
        if (ex) {
            e.preventDefault();
            input.value = ex.dataset.q;
            autosize();
            ask(ex.dataset.q, true);
            return;
        }
        var cp = e.target.closest("[data-copy]");
        if (cp) {
            copy(cp.dataset.copy, cp);
            return;
        }
        var expand = e.target.closest("[data-expand]");
        if (expand) {
            // -web-debug: open every category, filter and JSON block, or close them again
            var all = expand.closest(".debug").querySelectorAll("details");
            var open = expand.textContent !== "згорнути все";
            all.forEach(function (d) { d.open = open; });
            expand.textContent = open ? "згорнути все" : "розгорнути все";
        }
    });

    document.getElementById("history-clear").addEventListener("click", function () {
        saveHistory([]);
        renderHistory();
    });

    // switching the model re-runs the query on it
    radios.forEach(function (r) {
        r.addEventListener("change", function () {
            if (result.querySelector(".card") && input.value.trim()) {
                ask(input.value, true);
            }
        });
    });

    window.addEventListener("popstate", function (e) {
        var search = new URLSearchParams(location.search);
        var q = search.get("q") || "";
        var b = e.state && e.state.backend !== undefined ? e.state.backend : search.get("backend") || "";
        setBackend(b);
        input.value = q;
        autosize();
        document.title = (q ? q + " · " : "") + "OlxJev";
        result.innerHTML = cache[key(q, b)] || "";
    });

    autosize();
    // the first entry: what the server rendered, with the backend it checked
    history.replaceState({ q: input.value.trim(), backend: backend() }, "");
    var initial = result.querySelector(".card");
    if (initial) {
        cache[key(initial.dataset.query, backend())] = result.innerHTML;
        remember(initial);
    } else {
        renderHistory();
    }
    // put the caret at the end of a prefilled query
    input.setSelectionRange(input.value.length, input.value.length);
})();
