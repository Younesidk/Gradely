FROM mcr.microsoft.com/dotnet/sdk:10.0 AS build
WORKDIR /src
COPY Gradly.slnx ./
COPY src/Gradly/Gradly.csproj src/Gradly/
RUN dotnet restore src/Gradly/Gradly.csproj
COPY src/Gradly/ src/Gradly/
RUN dotnet publish src/Gradly/Gradly.csproj -c Release -o /app --no-restore

FROM mcr.microsoft.com/dotnet/aspnet:10.0 AS final
WORKDIR /app
COPY --from=build /app .
ENV ASPNETCORE_URLS=http://+:8080 \
    GRADLY_DATA_DIR=/data \
    GRADLY_SECURE_COOKIES=true
RUN mkdir -p /data && chown -R $APP_UID /data
VOLUME /data
EXPOSE 8080
USER $APP_UID
ENTRYPOINT ["dotnet", "Gradly.dll"]
