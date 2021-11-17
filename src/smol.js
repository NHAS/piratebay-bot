
window.addEventListener('load', function () {
    if (window.location.hash) {


        var parts = window.location.hash.split(":")
        if (parts.length == 2) {
            var div
            if (parts[0] == "#Error") {
                div = document.getElementById("sad");

            } else if (parts[0] == "#Success") {
                div = document.getElementById("happy");
            }

            div.textContent = decodeURI(parts[1]);
            div.style.display = 'block';

        }
        history.replaceState(null, null, ' ');
    }
})