/* Prevent FOUC: inline theme init before render */
(function(){try{var t=localStorage.getItem("su_theme");if(t==="light")document.documentElement.classList.remove("dark");else if(!t||t==="dark")document.documentElement.classList.add("dark")}catch(e){}})();
