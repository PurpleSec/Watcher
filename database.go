// Copyright (C) 2020 iDigitalFlame
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//

package watcher

var cleanStatements = []string{
	`DROP TABLES IF EXISTS Subscriptions`,
	`DROP TABLES IF EXISTS Mappings`,
	`DROP TABLES IF EXISTS Users`,
	`DROP FUNCTION IF EXISTS GetUserID`,
	`DROP PROCEDURE IF EXISTS GetSubscription`,
	`DROP PROCEDURE IF EXISTS AddSubscription`,
	`DROP PROCEDURE IF EXISTS RemoveSubscription`,
	`DROP PROCEDURE IF EXISTS GetAllSubscriptions`,
	`DROP PROCEDURE IF EXISTS RemoveAllSubscriptions`,
}

var setupStatements = []string{
	`CREATE TABLE IF NOT EXISTS Users(
		UserID BIGINT(64) NOT NULL PRIMARY KEY AUTO_INCREMENT,
		UserChat BIGINT(64) NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS Mappings(
		MapID BIGINT(64) NOT NULL PRIMARY KEY AUTO_INCREMENT,
		MapName VARCHAR(20) NOT NULL UNIQUE,
		MapTwitter BIGINT(64) NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS Subscribers(
		SubID BIGINT(64) NOT NULL PRIMARY KEY AUTO_INCREMENT,
		SubMap BIGINT(64) NOT NULL,
		SubUser BIGINT(64) NOT NULL,
		FOREIGN KEY(SubUser) REFERENCES Users(UserID),
		FOREIGN KEY(SubMap) REFERENCES Mappings(MapID)
	)`,
	`CREATE FUNCTION IF NOT EXISTS GetUserID(ChatID INT(64)) RETURNS INT(64) NOT DETERMINISTIC
	BEGIN
		SET @uid = COALESCE((SELECT UserID FROM Users WHERE UserChat = ChatID LIMIT 1), 0);
		IF @uid > 0 THEN
			RETURN @uid;
		ELSE
			INSERT INTO Users(UserChat) VALUES(ChatID);
			RETURN (SELECT UserID FROM Users WHERE UserChat = ChatID LIMIT 1);
		END IF;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS GetAllSubscriptions()
	BEGIN
		START TRANSACTION;
		DELETE FROM Mappings WHERE (SELECT COUNT(SubID) FROM Subscribers WHERE SubMap = MapID) = 0;
		COMMIT;
		SELECT MapID, MapName, MapTwitter FROM Mappings;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS GetSubscription(ChatID INT(64))
	BEGIN
		SELECT M.MapName FROM Mappings M
			INNER JOIN Subscribers S ON M.MapID = S.SubMap
		WHERE S.SubUser = (SELECT GetUs(ChatID));
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS RemoveAllSubscriptions(ChatID INT(64))
	BEGIN
		SET @uid = (SELECT GetUserID(ChatID));
		START TRANSACTION;
		DELETE FROM Subscribers WHERE SubUser = @uid;
		DELETE FROM Mappings WHERE (SELECT COUNT(SubID) FROM Subscribers WHERE SubMap = MapID) = 0;
		COMMIT;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS AddSubscription(ChatID INT(64), Name VARCHAR(20))
	BEGIN
		SET @uid = (SELECT GetUserID(ChatID));
		SET @exists = COALESCE(
			(SELECT M.MapID FROM Mappings M
				INNER JOIN Subscribers S ON M.MapID = S.SubMap
			WHERE S.SubUser = @uid AND M.MapName = Name), 0
		);
		START TRANSACTION;
		SET @mid = COALESCE((SELECT MapID FROM Mappings WHERE MapName = Name LIMIT 1), 0);
		IF @mid = 0 THEN
			INSERT INTO Mappings(MapName) VALUES(Name);
			SET @mid = (SELECT MapID FROM Mappings WHERE MapName = Name LIMIT 1);
		END IF;
		IF @exists = 0 THEN
			INSERT INTO Subscribers(SubMap, SubUser) VALUES(@mid, @uid);
		END IF;
		COMMIT;
		SELECT MapTwitter FROM Mappings WHERE MapID = @mid;
	END;`,
	`CREATE PROCEDURE IF NOT EXISTS RemoveSubscription(ChatID INT(64), Name VARCHAR(20))
	BEGIN
		SET @uid = (SELECT GetUserID(ChatID));
		SET @exists = COALESCE(
			(SELECT M.MapID FROM Mappings M
				INNER JOIN Subscribers S ON M.MapID = S.SubMap
			WHERE S.SubUser = @uid AND M.MapName = Name LIMIT 1), 0
		);
		START TRANSACTION;
		SET @mid = COALESCE((SELECT MapID FROM Mappings WHERE MapName = Name LIMIT 1), 0);
		IF @mid > 0 THEN
			DELETE FROM Subscribers WHERE SubMap = @mid AND SubUser = @uid;
		END IF;
		SET @map_num = COALESCE((SELECT COUNT(SubMap) FROM Subscribers WHERE SubMap = @mid), 0);
		IF @map_num = 0 THEN
			DELETE FROM Mappings WHERE MapID = @mid;
		END IF;
		COMMIT;
	END;`,
}

var queryStatements = map[string]string{
	"get":      `CALL GetSubscription(?)`,
	"add":      `CALL AddSubscription(?, ?)`,
	"del":      `CALL RemoveSubscription(?, ?)`,
	"set":      `UPDATE Mappings SET MapTwitter = ? WHERE MapID = ?`,
	"del_all":  `CALL RemoveAllSubscriptions(?)`,
	"get_all":  `CALL GetAllSubscriptions()`,
	"get_list": `SELECT MapTwitter FROM Mappings`,
	"get_notify": `SELECT U.UserChat FROM Users U
						INNER JOIN Subscribers S ON S.SubUser = U.UserID
						INNER JOIN Mappings M ON M.MapID = S.SubMap
					WHERE M.MapTwitter = ?`,
}
